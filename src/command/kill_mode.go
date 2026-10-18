package command

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
)

// KillMode selects which processes a stop signal reaches.
type KillMode string

const (
	// KillModeParent signals only the launched process.
	KillModeParent KillMode = "parent"
	// KillModeGroup signals the process group of the launched process (default).
	KillModeGroup KillMode = "group"
	// KillModeTree signals the launched process and every descendant, including
	// descendants that left its process group or session, or were reparented
	// after their parent exited. Windows treats it like KillModeGroup.
	KillModeTree KillMode = "tree"
)

// killModes are the valid kill modes.
var killModes = []KillMode{KillModeParent, KillModeGroup, KillModeTree}

func (m KillMode) Validate() error {
	if slices.Contains(killModes, m) {
		return nil
	}
	return fmt.Errorf("invalid kill_mode '%s': must be one of %s, %s, %s", m, KillModeParent, KillModeGroup, KillModeTree)
}

// Schema is the HTTP API schema of a kill mode.
func (KillMode) Schema(huma.Registry) *huma.Schema {
	modes := make([]any, len(killModes))
	for i, mode := range killModes {
		modes[i] = mode
	}
	return &huma.Schema{Type: huma.TypeString, Enum: modes}
}

// TreeMarkerEnvName is the environment variable that marks every process
// launched in tree kill mode with a per-launch ID. Descendants inherit it, so a
// tree stop can find descendants that no parent link or process group leads
// to. A descendant that clears or overrides it (e.g. a nested process-compose
// launching its own tree-mode processes) is only found through parent links.
const TreeMarkerEnvName = "PC_PROCESS_TREE_ID"

// WithTreeMarker returns env with a fresh tree marker, replacing any marker
// inherited from a parent process-compose. A nil env means the current
// process environment, as in exec.Cmd.
func WithTreeMarker(env []string) []string {
	if env == nil {
		env = os.Environ()
	}
	marked := make([]string, 0, len(env)+1)
	for _, kv := range env {
		if !strings.HasPrefix(kv, TreeMarkerEnvName+"=") {
			marked = append(marked, kv)
		}
	}
	return append(marked, TreeMarkerEnvName+"="+uuid.NewString())
}

// treeMarker returns the "NAME=ID" marker entry in env, or "" when the process
// was not launched in tree kill mode.
func treeMarker(env []string) string {
	for i := len(env) - 1; i >= 0; i-- {
		if strings.HasPrefix(env[i], TreeMarkerEnvName+"=") {
			return env[i]
		}
	}
	return ""
}

// treeState remembers the members found by tree-mode stops of one launched
// process. A later stop, such as the SIGKILL fallback after the launched
// process exited, uses them to reach members that lost their parent link.
type treeState struct {
	mu sync.Mutex
	// rootStart is the start-time token of the launched process, recorded on
	// the first walk so that a reused PID is not mistaken for it.
	rootStart uint64
	rootSeen  bool
	// known maps member PIDs to their start-time token, to detect PID reuse.
	known map[int]uint64
	// marked caches tree-marker lookups per process incarnation.
	marked map[procKey]bool
}

type procKey struct {
	pid   int
	start uint64
}
