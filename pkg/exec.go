package mariadb

import (
	"bytes"
	helper "github.com/openstack-k8s-operators/lib-common/modules/common/helper"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
	"k8s.io/kubectl/pkg/scheme"
)

// ExecInPod - execs a command in a running pod and pass its output to a callback function
func ExecInPod(h *helper.Helper, config *rest.Config, namespace string, pod string, container string, cmd []string, fun func(*bytes.Buffer, *bytes.Buffer) error) error {
	req := h.GetKClient().CoreV1().RESTClient().Post().Resource("pods").Name(pod).Namespace(namespace).SubResource("exec").Param("container", container)
	req.VersionedParams(
		&corev1.PodExecOptions{
			Command: cmd,
			Stdin:   false,
			Stdout:  true,
			Stderr:  true,
			TTY:     false,
		},
		scheme.ParameterCodec,
	)

	exec, err := remotecommand.NewSPDYExecutor(config, "POST", req.URL())
	if err != nil {
		return err
	}

	var stdout, stderr bytes.Buffer
	err = exec.Stream(remotecommand.StreamOptions{
		Stdin:  nil,
		Stdout: &stdout,
		Stderr: &stderr,
		Tty:    false,
	})

	if err != nil {
		return err
	}

	return fun(&stdout, &stderr)
}
