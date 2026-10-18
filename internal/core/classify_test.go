package core

import (
	"testing"
)

func intPtr(value int) *int { return &value }

func TestClassifyCategories(t *testing.T) {
	tests := []struct {
		name  string
		input ClassifyInput
		want  Category
	}{
		// §1 class 1: environment, toolchain and runtime.
		{
			name:  "missing command",
			input: ClassifyInput{Source: "command", Message: "bash: line 1: node: command not found", ExitCode: intPtr(127)},
			want:  CategoryEnvironment,
		},
		{
			name:  "missing executable",
			input: ClassifyInput{Message: "spawn pnpm ENOENT"},
			want:  CategoryEnvironment,
		},
		{
			name:  "runtime panic",
			input: ClassifyInput{Message: "panic: runtime error: index out of range [3] with length 3"},
			want:  CategoryEnvironment,
		},
		{
			name:  "unsupported platform",
			input: ClassifyInput{Message: "unsupported platform: windows/386"},
			want:  CategoryEnvironment,
		},

		// §1 class 2: dependency, version, configuration, authentication,
		// permission and network.
		{
			name:  "unauthorized",
			input: ClassifyInput{Message: "401 Unauthorized: invalid token"},
			want:  CategoryDependency,
		},
		{
			name:  "permission denied",
			input: ClassifyInput{Message: "EACCES: permission denied, open '/etc/errkb.json'"},
			want:  CategoryDependency,
		},
		{
			name:  "dns lookup",
			input: ClassifyInput{Message: "getaddrinfo ENOTFOUND registry.example"},
			want:  CategoryDependency,
		},
		{
			name:  "dependency resolution",
			input: ClassifyInput{Message: "npm ERR! ERESOLVE unable to resolve dependency tree"},
			want:  CategoryDependency,
		},
		{
			name:  "llm source",
			input: ClassifyInput{Source: "llm", Message: "the adapter gave up"},
			want:  CategoryDependency,
		},
		{
			name:  "llm failure code",
			input: ClassifyInput{Source: "llm", Code: "RATE_LIMIT", Message: "slow down"},
			want:  CategoryDependency,
		},

		// §1 class 3: compile, test, code-run and command-exit.
		{
			name:  "typescript error",
			input: ClassifyInput{Message: "src/a.ts(3,5): error TS2345: Argument of type 'string' is not assignable"},
			want:  CategoryBuild,
		},
		{
			name:  "test assertion",
			input: ClassifyInput{Message: "AssertionError: expected 1 to equal 2"},
			want:  CategoryBuild,
		},
		{
			name:  "non-zero exit code",
			input: ClassifyInput{Source: "command", Command: "pnpm test", ExitCode: intPtr(2)},
			want:  CategoryBuild,
		},
		{
			name:  "command source without other evidence",
			input: ClassifyInput{Source: "command", Command: "make"},
			want:  CategoryBuild,
		},

		// §1 class 4: tool call, plugin, hook, MCP and protocol.
		{
			name:  "tool source",
			input: ClassifyInput{Source: "tool", Message: "the file does not exist"},
			want:  CategoryToolCall,
		},
		{
			name:  "tool name",
			input: ClassifyInput{Tool: "read_file", Message: "failed"},
			want:  CategoryToolCall,
		},
		{
			name:  "mcp protocol error",
			input: ClassifyInput{Message: "MCP server returned protocol error: invalid params"},
			want:  CategoryToolCall,
		},
		{
			name:  "hook failure",
			input: ClassifyInput{Message: "hook session-start failed"},
			want:  CategoryToolCall,
		},

		// Unknown, explicitly.
		{
			name:  "empty input",
			input: ClassifyInput{},
			want:  CategoryUnknown,
		},
		{
			name:  "unexplained message",
			input: ClassifyInput{Message: "everything was fine until it was not"},
			want:  CategoryUnknown,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := Classify(test.input).Category; got != test.want {
				t.Errorf("Classify(%+v).Category = %q, want %q", test.input, got, test.want)
			}
		})
	}
}

// TestClassifyUnknownIsExplicit proves unknown is a real category and is not
// silently folded into a concrete one: the same inputs classify as unknown and
// the unknown value is not equal to any concrete category.
func TestClassifyUnknownIsExplicit(t *testing.T) {
	got := Classify(ClassifyInput{Message: "something happened"}).Category
	if got != CategoryUnknown {
		t.Fatalf("Category = %q, want %q", got, CategoryUnknown)
	}
	for _, concrete := range []Category{
		CategoryEnvironment,
		CategoryDependency,
		CategoryBuild,
		CategoryToolCall,
	} {
		if got == concrete {
			t.Errorf("unknown equals concrete category %q", concrete)
		}
	}
}

// TestClassifyPriority covers the documented first-match order.
func TestClassifyPriority(t *testing.T) {
	// Tool evidence outranks the auth text in the same message.
	got := Classify(ClassifyInput{Source: "tool", Message: "401 Unauthorized"}).Category
	if got != CategoryToolCall {
		t.Errorf("tool source with auth text = %q, want %q", got, CategoryToolCall)
	}

	// Environment evidence outranks the non-zero exit code it caused.
	got = Classify(ClassifyInput{Source: "command", Message: "go: command not found", ExitCode: intPtr(127)}).Category
	if got != CategoryEnvironment {
		t.Errorf("missing command with exit code = %q, want %q", got, CategoryEnvironment)
	}

	// Dependency evidence outranks a non-zero exit code from the command layer.
	got = Classify(ClassifyInput{Source: "command", Command: "curl", Message: "connection refused", ExitCode: intPtr(7)}).Category
	if got != CategoryDependency {
		t.Errorf("command with network text = %q, want %q", got, CategoryDependency)
	}

	// Channel levels are not categories (§3.1 is orthogonal). Source "A" is not
	// evidence of a category.
	got = Classify(ClassifyInput{Source: "A", Message: "mystery"}).Category
	if got != CategoryUnknown {
		t.Errorf("channel level used as category: got %q, want %q", got, CategoryUnknown)
	}
}

func TestClassifyTransient(t *testing.T) {
	tests := []struct {
		name  string
		input ClassifyInput
		want  bool
	}{
		{name: "permanent code auth", input: ClassifyInput{Source: "llm", Code: "AUTH", Message: "request timed out"}, want: false},
		{name: "permanent code quota", input: ClassifyInput{Source: "llm", Code: "QUOTA", Message: "rate limit reached"}, want: false},
		{name: "permanent code unknown", input: ClassifyInput{Source: "llm", Code: "UNKNOWN", Message: "rate limit"}, want: false},
		{name: "transient code rate limit", input: ClassifyInput{Code: "RATE_LIMIT"}, want: true},
		{name: "transient code with dash", input: ClassifyInput{Code: "rate-limit"}, want: true},
		{name: "transient code server", input: ClassifyInput{Code: "SERVER"}, want: true},
		{name: "transient code timeout", input: ClassifyInput{Code: "TIMEOUT"}, want: true},
		{name: "transient code transport", input: ClassifyInput{Code: "TRANSPORT"}, want: true},
		{name: "transient code empty response", input: ClassifyInput{Code: "EMPTY_RESPONSE"}, want: true},
		{name: "transient text rate limit", input: ClassifyInput{Message: "rate limit exceeded"}, want: true},
		{name: "transient text 429", input: ClassifyInput{Message: "HTTP 429"}, want: true},
		{name: "transient text timeout", input: ClassifyInput{Message: "request timed out"}, want: true},
		{name: "transient text connection reset", input: ClassifyInput{Message: "connection reset by peer"}, want: true},
		{name: "transient text service unavailable", input: ClassifyInput{Message: "503 Service Unavailable"}, want: true},
		{name: "transient text internal server error", input: ClassifyInput{Message: "internal server error"}, want: true},
		{name: "transient text empty response", input: ClassifyInput{Message: "empty response from model"}, want: true},
		{name: "permanent text not found", input: ClassifyInput{Message: "getaddrinfo ENOTFOUND api.example"}, want: false},
		{name: "permanent text auth beats timeout", input: ClassifyInput{Message: "401 Unauthorized (request timed out)"}, want: false},
		{name: "permanent text invalid key", input: ClassifyInput{Message: "invalid api key"}, want: false},
		{name: "permanent text permission", input: ClassifyInput{Message: "EACCES: permission denied"}, want: false},
		{name: "default is permanent", input: ClassifyInput{Message: "boom"}, want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := Classify(test.input)
			if got.Transient != test.want {
				t.Errorf("Classify(%+v).Transient = %v, want %v", test.input, got.Transient, test.want)
			}
			wantSeverity := SeverityError
			if test.want {
				wantSeverity = SeverityWarning
			}
			if got.Severity != wantSeverity {
				t.Errorf("Classify(%+v).Severity = %q, want %q", test.input, got.Severity, wantSeverity)
			}
		})
	}
}

// TestClassifyCrossCombinations covers representative category/transient pairs.
func TestClassifyCrossCombinations(t *testing.T) {
	tests := []struct {
		name      string
		input     ClassifyInput
		category  Category
		transient bool
	}{
		{
			name:      "dependency transient",
			input:     ClassifyInput{Source: "llm", Code: "RATE_LIMIT", Message: "rate limit exceeded"},
			category:  CategoryDependency,
			transient: true,
		},
		{
			name:      "dependency permanent",
			input:     ClassifyInput{Message: "401 Unauthorized"},
			category:  CategoryDependency,
			transient: false,
		},
		{
			name:      "build permanent",
			input:     ClassifyInput{Message: "src/a.ts(3,5): error TS2345"},
			category:  CategoryBuild,
			transient: false,
		},
		{
			name:      "tool call permanent",
			input:     ClassifyInput{Message: "invalid tool: nope"},
			category:  CategoryToolCall,
			transient: false,
		},
		{
			name:      "environment permanent",
			input:     ClassifyInput{Message: "go: command not found"},
			category:  CategoryEnvironment,
			transient: false,
		},
		{
			name:      "unknown permanent",
			input:     ClassifyInput{Message: "mystery"},
			category:  CategoryUnknown,
			transient: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := Classify(test.input)
			if got.Category != test.category || got.Transient != test.transient {
				t.Errorf("Classify(%+v) = {%q, %v}, want {%q, %v}", test.input, got.Category, got.Transient, test.category, test.transient)
			}
		})
	}
}

// TestClassifyIsDeterministic is the purity check: the same input gives the same
// answer, and no global counter changes it.
func TestClassifyIsDeterministic(t *testing.T) {
	input := ClassifyInput{Source: "llm", Code: "TIMEOUT", Message: "request timed out"}
	first := Classify(input)
	for i := 0; i < 10; i++ {
		if got := Classify(input); got != first {
			t.Fatalf("call %d differs: %+v vs %+v", i+2, got, first)
		}
	}
}
