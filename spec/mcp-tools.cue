// The MCP tool contract skeleton: a host asks for a known fix through these
// tools. EKB-037 fills in the tool list.
package spec

#Tool: {
	name:        string
	description: string
	read_only:   bool
	input: {...}
	output: {...}
}
