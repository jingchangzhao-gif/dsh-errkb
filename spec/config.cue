// The user-editable configuration contract. People write YAML or JSON; this
// file is the internal validation shape only, so nobody has to learn CUE to
// configure errkb. Defaults below are the safe ones (§6).
package spec

#Config: {
	// Development connects to an external PostgreSQL 16 instance (plan §2).
	database: {
		dsn: string & !=""
		max_connections?: int & >0
		connect_timeout_ms?: int & >0
	}

	service: {
		// A Named Pipe is the default local channel (§6).
		pipe: {
			name?: string & !=""
			enabled: bool | *true
		}
		// Loopback HTTP stays off until the user enables it; Go then also
		// requires a random token (§6).
		http: {
			enabled: bool | *false
			host?: string & !=""
			port?: int & >0 & <65536
			token?: string
		}
	}

	capture: {
		enabled: bool | *true
		// Pausing is per connector and per project (§6).
		paused_connectors?: [...string]
		paused_projects?: [...string]
	}

	notices: {
		// Returning a known fix before the host diagnoses. Go rejects this
		// while capture is off: there is nothing to return.
		before_diagnosis: bool | *true
	}

	// The three retention periods are configured separately (§6).
	retention: {
		sample_days:     int & >=0
		normalized_days: int & >=0
		audit_days:      int & >=0
	}

	redaction: {
		extra_patterns?: [...string]
	}

	search: {
		// §5 returns at most three candidates.
		limit: int & >=1 & <=3 | *3
	}

	timeout: {
		capture_ms: int & >0 | *2000
		search_ms:  int & >0 | *1500
		mcp_ms:     int & >0 | *5000
	}

	offline: {
		// While PostgreSQL is unreachable the daemon spools here and replays.
		spool_dir?: string
		replay_interval_ms: int & >0 | *5000
	}
}
