package mariadb

const (
	// ServiceName -
	ServiceName = "mariadb"

	// ActivePodSelectorKey - Selector key used to configure A/P service behavior
	ActivePodSelectorKey = "statefulset.kubernetes.io/pod-name"

	// Time allowed during a the startup probe (in seconds)
	StartupProbeTimeout = 240
)
