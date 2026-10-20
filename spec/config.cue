// The user-editable configuration contract. People write YAML or JSON; this
// file is the internal validation shape only, so nobody has to learn CUE to
// configure errkb. Defaults below are the safe ones (§6).
package spec

#Config: {
	// Development connects to an external PostgreSQL 16 instance (plan §2).
	database: {
		dsn:                 string & !=""
		max_connections?:    int & >0
		connect_timeout_ms?: int & >0
	}

	service: {
		// A Named Pipe is the default local channel (§6).
		pipe: {
			name?:   string & !=""
			enabled: bool | *true
		}
		// Loopback HTTP stays off until the user enables it; Go then also
		// requires a random token (§6).
		http: {
			enabled: bool | *false
			host?:   string & !=""
			port?:   int & >0 & <65536
			token?:  string
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
		spool_dir?:         string
		replay_interval_ms: int & >0 | *5000
	}

	// Generic host templates (§3.2, EKB-042). They describe how a host without
	// a stable, testable interface is connected through the generic channels:
	// B (CLI wrapper), C (log tail) or E (manual entry). They never imply a
	// named adapter.
	templates?: {
		cli?: {
			enabled: bool | *true
			command: [...string]
			capture_exit_codes: bool | *true
		}
		log?: {
			enabled:  bool | *false
			path:     string & !=""
			encoding: "utf-8" | "utf-16le" | "utf-16be" | *"utf-8"
			rotation?: {
				max_bytes: int & >0 | *10485760
				keep:      int & >=0 | *3
			}
		}
		manual?: {
			enabled: bool | *true
			note?:   string
		}
	}
}
