package mariadb

const (
	// ServiceName -
	ServiceName = "mariadb"

	// ActivePodSelectorKey - Selector key used to configure A/P service behavior
	ActivePodSelectorKey = "statefulset.kubernetes.io/pod-name"

	// StartupProbeTimeout is the time allowed during the startup probe (in seconds)
	StartupProbeTimeout = 240

	// MysqlUID is the UID/GID of the mysql user in the container image (from TCIB)
	MysqlUID int64 = 42434
)
