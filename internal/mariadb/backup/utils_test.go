package mariadbbackup

import (
	"testing"

	mariadbv1 "github.com/openstack-k8s-operators/mariadb-operator/api/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestTruncateWithHash(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		maxLen    int
		wantLen   int
		wantExact string
	}{
		{
			name:      "short name passes through unchanged",
			input:     "db1-backup-daily",
			maxLen:    47,
			wantExact: "db1-backup-daily",
		},
		{
			name:    "exactly at limit passes through unchanged",
			input:   "openstack-nova-cell1-backup-openstack-nova-cell",
			maxLen:  47,
			wantLen: 47,
		},
		{
			name:    "one over limit gets truncated with hash",
			input:   "openstack-nova-cell1-backup-openstack-nova-cell",
			maxLen:  47,
			wantLen: 47,
		},
		{
			name:    "long name gets truncated with hash",
			input:   "openstack-nova-cell1-backup-openstack-nova-cell1",
			maxLen:  47,
			wantLen: 47,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncateWithHash(tt.input, tt.maxLen)
			if tt.wantExact != "" {
				if got != tt.wantExact {
					t.Errorf("truncateWithHash(%q, %d) = %q, want %q", tt.input, tt.maxLen, got, tt.wantExact)
				}
			}
			if tt.wantLen > 0 && len(got) != tt.wantLen {
				t.Errorf("truncateWithHash(%q, %d) length = %d, want %d", tt.input, tt.maxLen, len(got), tt.wantLen)
			}
			if len(got) > tt.maxLen {
				t.Errorf("truncateWithHash(%q, %d) = %q (%d chars), exceeds maxLen %d", tt.input, tt.maxLen, got, len(got), tt.maxLen)
			}
		})
	}
}

func TestTruncateWithHashUniqueness(t *testing.T) {
	a := truncateWithHash("openstack-nova-cell1-backup-openstack-nova-cell1", 47)
	b := truncateWithHash("openstack-nova-cell1-backup-openstack-nova-cell1-hourly", 47)
	if a == b {
		t.Errorf("different inputs produced the same output: %q", a)
	}
}

func TestTruncateWithHashDeterministic(t *testing.T) {
	input := "openstack-nova-cell1-backup-openstack-nova-cell1"
	a := truncateWithHash(input, 47)
	b := truncateWithHash(input, 47)
	if a != b {
		t.Errorf("same input produced different outputs: %q vs %q", a, b)
	}
}

func TestBackupCronJobName(t *testing.T) {
	tests := []struct {
		name       string
		galeraName string
		backupName string
		wantExact  string
		wantMaxLen int
	}{
		{
			name:       "short names are not truncated",
			galeraName: "db1",
			backupName: "daily",
			wantExact:  "db1-backup-daily",
		},
		{
			name:       "long names are truncated to fit label limit",
			galeraName: "openstack-nova-cell1",
			backupName: "openstack-nova-cell1",
			wantMaxLen: backupJobMaxLabelLen,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := &mariadbv1.GaleraBackup{
				ObjectMeta: metav1.ObjectMeta{Name: tt.backupName},
			}
			g := &mariadbv1.Galera{
				ObjectMeta: metav1.ObjectMeta{Name: tt.galeraName},
			}
			got := BackupCronJobName(b, g)
			if tt.wantExact != "" && got != tt.wantExact {
				t.Errorf("BackupCronJobName() = %q, want %q", got, tt.wantExact)
			}
			if len(got) > backupJobMaxLabelLen {
				t.Errorf("BackupCronJobName() = %q (%d chars), exceeds max %d",
					got, len(got), backupJobMaxLabelLen)
			}
		})
	}
}
