package functional_test

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2" //revive:disable:dot-imports
	. "github.com/onsi/gomega"    //revive:disable:dot-imports
	"github.com/openstack-k8s-operators/lib-common/modules/common/util"
	"github.com/openstack-k8s-operators/mariadb-operator/internal/mariadb"
)

func tlsRuntimeCommand(policy, program string, args ...string) (string, error) {
	commandCtx, commandCancel := context.WithTimeout(ctx, 15*time.Second)
	defer commandCancel()
	// #nosec G204 -- Programs and arguments are selected by these test fixtures.
	command := exec.CommandContext(commandCtx, program, args...)
	command.Env = append(os.Environ(), "OPENSSL_CONF="+policy)
	output, err := command.CombinedOutput()
	return string(output), err
}

var _ = Describe("TLS profile runtime enforcement", func() {
	It("negotiates the inherited PQC group and cipher and honors service overrides", func() {
		for _, program := range []string{"openssl", "socat", "mariadbd", "mariadb-install-db"} {
			if _, err := exec.LookPath(program); err != nil {
				Skip("runtime TLS test requires " + program)
			}
		}
		const systemConfig = "/etc/pki/tls/openssl.cnf"
		if _, err := os.Stat(systemConfig); err != nil {
			Skip("runtime TLS test requires the RHEL OpenSSL configuration layout")
		}
		groups, err := tlsRuntimeCommand(systemConfig, "openssl", "list", "-tls1_3", "-tls-groups")
		if err != nil || !strings.Contains(groups, "X25519MLKEM768") {
			Skip("runtime TLS test requires OpenSSL with X25519MLKEM768 support")
		}
		root := GinkgoT().TempDir()
		cert := filepath.Join(root, "server.crt")
		key := filepath.Join(root, "server.key")
		output, err := tlsRuntimeCommand(systemConfig, "openssl", "req", "-x509", "-newkey", "rsa:2048", "-nodes",
			"-keyout", key, "-out", cert, "-days", "1", "-subj", "/CN=localhost")
		Expect(err).NotTo(HaveOccurred(), output)

		for _, tc := range []struct {
			name, native, version, cipher, group string
		}{
			{"inherited", "", "TLSv1.3", "TLS_AES_128_GCM_SHA256", "X25519MLKEM768"},
			{"service override", "[mysqld]\ntls_version=TLSv1.2\nssl_cipher=ECDHE-RSA-AES256-GCM-SHA384\n",
				"TLSv1.2", "ECDHE-RSA-AES256-GCM-SHA384", "X25519"},
		} {
			By("checking " + tc.name)
			values := map[string]any{
				"TLSConfigOptions": mariadb.NewTLSConfigOptions,
				"MinTLSVersion":    "VersionTLS13", "SSLCipherSuite": "TLS_AES_128_GCM_SHA256",
				"TLSGroups": tc.group, "customServiceConfig": tc.native,
			}
			policy, err := util.ExecuteTemplateFile("openssl-profile.cnf", values)
			Expect(err).NotTo(HaveOccurred())
			serverConfig, err := util.ExecuteTemplateFile("galera/config/galera_tls.cnf.in", values)
			Expect(err).NotTo(HaveOccurred())
			var socketOptions string
			for line := range strings.SplitSeq(serverConfig, "\n") {
				if options, found := strings.CutPrefix(line, "sockopt = "); found {
					socketOptions = options
				}
			}
			Expect(socketOptions).To(ContainSubstring("openssl-groups=" + tc.group))
			policyPath := filepath.Join(root, strings.ReplaceAll(tc.name, " ", "-")+".cnf")
			Expect(os.WriteFile(policyPath, []byte(policy), 0600)).To(Succeed())

			for _, service := range []string{"socat", "mariadbd"} {
				func() {
					By("negotiating TLS with " + service + " using " + tc.name)
					listener, err := net.Listen("tcp", "127.0.0.1:0")
					Expect(err).NotTo(HaveOccurred())
					address := listener.Addr().String()
					port := fmt.Sprint(listener.Addr().(*net.TCPAddr).Port)
					Expect(listener.Close()).To(Succeed())
					var args []string
					if service == "socat" {
						options, err := tlsRuntimeCommand(systemConfig, "socat", "-hhh")
						Expect(err).NotTo(HaveOccurred(), options)
						if !strings.Contains(options, "openssl-groups") {
							output, err := tlsRuntimeCommand(policyPath, "socat", "OPENSSL-LISTEN:"+port+",openssl-groups="+tc.group, "-")
							Expect(err).To(HaveOccurred(), output)
							Expect(output).To(ContainSubstring("unknown option"))
							GinkgoWriter.Println("socat lacks openssl-groups; verified rejection, skipping its positive handshake")
							return
						}
						args = []string{"OPENSSL-LISTEN:" + port + ",bind=127.0.0.1,reuseaddr,fork,verify=0,cert=" + cert +
							",key=" + key + "," + socketOptions, "EXEC:cat"}
					} else {
						data := filepath.Join(root, strings.ReplaceAll(tc.name, " ", "-")+"-data")
						output, err := tlsRuntimeCommand(systemConfig, "mariadb-install-db", "--no-defaults", "--datadir="+data,
							"--auth-root-authentication-method=normal", "--skip-test-db")
						Expect(err).NotTo(HaveOccurred(), output)
						args = []string{"--no-defaults", "--datadir=" + data, "--socket=" + data + "/mysql.sock",
							"--pid-file=" + data + "/mysql.pid", "--bind-address=127.0.0.1", "--port=" + port,
							"--skip-grant-tables", "--skip-name-resolve", "--ssl-cert=" + cert, "--ssl-key=" + key}
						if tc.native != "" {
							args = append(args, "--tls-version="+tc.version, "--ssl-cipher="+tc.cipher)
						}
					}
					logPath := filepath.Join(root, strings.ReplaceAll(tc.name, " ", "-")+"-"+service+".log")
					// #nosec G304 -- The log path is inside Ginkgo's temporary directory and uses fixed fixture names.
					logFile, err := os.Create(logPath)
					Expect(err).NotTo(HaveOccurred())
					defer func() { _ = logFile.Close() }()
					processCtx, processCancel := context.WithCancel(ctx)
					// #nosec G204 -- Only the fixed socat and mariadbd test fixtures are started.
					process := exec.CommandContext(processCtx, service, args...)
					process.Env = append(os.Environ(), "OPENSSL_CONF="+policyPath)
					process.Stdout, process.Stderr = logFile, logFile
					Expect(process.Start()).To(Succeed())
					defer func() { processCancel(); _ = process.Wait() }()
					Eventually(func(g Gomega) {
						connection, err := net.DialTimeout("tcp", address, time.Second)
						// #nosec G304 -- Read the fixed fixture log created in the temporary directory above.
						logs, _ := os.ReadFile(logPath)
						g.Expect(err).NotTo(HaveOccurred(), string(logs))
						if connection != nil {
							_ = connection.Close()
						}
					}, 15*time.Second, 100*time.Millisecond).Should(Succeed())
					clientArgs := []string{"s_client", "-connect", address, "-brief", "-CAfile", cert, "-verify_return_error"}
					if service == "mariadbd" {
						clientArgs = append(clientArgs, "-starttls", "mysql")
					}
					negotiated, err := tlsRuntimeCommand(systemConfig, "openssl", clientArgs...)
					Expect(err).NotTo(HaveOccurred(), negotiated)
					Expect(negotiated).To(ContainSubstring("Protocol version: " + tc.version))
					Expect(negotiated).To(ContainSubstring("Ciphersuite: " + tc.cipher))
					Expect(negotiated).To(ContainSubstring(tc.group))
					if tc.name == "inherited" {
						for _, rejected := range [][]string{{"-tls1_2"}, {"-tls1_3", "-groups", "X25519"},
							{"-tls1_3", "-ciphersuites", "TLS_AES_256_GCM_SHA384"}} {
							attempt := append(append([]string{}, clientArgs...), rejected...)
							output, err := tlsRuntimeCommand(systemConfig, "openssl", attempt...)
							Expect(err).To(HaveOccurred(), "accepted settings excluded by the profile: "+output)
						}
					}
				}()
			}
		}

		By("rejecting unsupported groups instead of ignoring the policy")
		invalid, err := util.ExecuteTemplateFile("openssl-profile.cnf", map[string]any{
			"TLSConfigOptions": mariadb.NewTLSConfigOptions, "TLSGroups": "NoSuchGroup",
		})
		Expect(err).NotTo(HaveOccurred())
		invalidPath := filepath.Join(root, "invalid.cnf")
		Expect(os.WriteFile(invalidPath, []byte(invalid), 0600)).To(Succeed())
		output, err = tlsRuntimeCommand(invalidPath, "openssl", "ciphers", "-s")
		Expect(err).To(HaveOccurred(), output)
	})
})
