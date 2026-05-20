package session

// SendInput carries all parameters for Manager.Send.
type SendInput struct {
	Input      string // empty = continue waiting for in-flight marker
	TimeoutMs  int
	Raw        bool // skip echo/ANSI stripping and marker injection
	WaitIdleMs int  // peek mode; mutually exclusive with Input
}

// SendResult is the structured output of Manager.Send.
type SendResult struct {
	Output       string
	ExitCode     *int // nil if still_running=true or raw=true
	StillRunning bool // marker not yet received
	Truncated    bool
	ClosedReason string // "" | "exit" | "lost" | "idle" | "max_age" | "shutdown" | "explicit"
	Warnings     []string
}

// RunOnceInput carries parameters for Manager.RunOnce.
type RunOnceInput struct {
	Command   string
	Stdin     string
	Env       map[string]string
	TimeoutMs int
}

// RunOnceOutput is the structured result of Manager.RunOnce.
type RunOnceOutput struct {
	Stdout    string
	Stderr    string
	ExitCode  int
	Signal    string // non-empty if killed by signal
	Truncated bool
	Warnings  []string
}
