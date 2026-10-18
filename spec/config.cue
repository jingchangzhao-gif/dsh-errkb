// The daemon and connector configuration skeleton. Defaults and ranges are
// finalized with the settings card; EKB-005 only needs a schema `cue vet`
// accepts.
package spec

#Config: {
	database: {
		dsn: string
	}
	search: {
		limit: int
	}
	capture: {
		channels: [...string]
	}
	auto_apply: bool
}
