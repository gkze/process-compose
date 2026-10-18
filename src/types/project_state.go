package types

import "time"

type ProjectState struct {
	FileNames         []string      `json:"fileNames" required:"true"`
	UpTime            time.Duration `json:"upTime" required:"true"`
	StartTime         time.Time     `json:"startTime" required:"true"`
	ProcessNum        int           `json:"processNum" required:"true"`
	RunningProcessNum int           `json:"runningProcessNum" required:"true"`
	UserName          string        `json:"userName" required:"true"`
	Version           string        `json:"version" required:"true"`
	ProjectName       string        `json:"projectName" required:"true"`
	MemoryState       *MemoryState  `json:"memoryState,omitempty"`
}

type MemoryState struct {
	Allocated      uint64 `json:"allocated" required:"true"`
	TotalAllocated uint64 `json:"totalAllocated" required:"true"`
	SystemMemory   uint64 `json:"systemMemory" required:"true"`
	GcCycles       uint32 `json:"gcCycles" required:"true"`
}
