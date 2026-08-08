package types

import "time"

type ProjectState struct {
	FileNames         []string      `json:"fileNames" binding:"required"`
	UpTime            time.Duration `json:"upTime" binding:"required" swaggertype:"primitive,integer"`
	StartTime         time.Time     `json:"startTime" binding:"required"`
	ProcessNum        int           `json:"processNum" binding:"required"`
	RunningProcessNum int           `json:"runningProcessNum" binding:"required"`
	UserName          string        `json:"userName" binding:"required"`
	Version           string        `json:"version" binding:"required"`
	ProjectName       string        `json:"projectName" binding:"required"`
	MemoryState       *MemoryState  `json:"memoryState,omitempty"`
}

type MemoryState struct {
	Allocated      uint64 `json:"allocated" binding:"required"`
	TotalAllocated uint64 `json:"totalAllocated" binding:"required"`
	SystemMemory   uint64 `json:"systemMemory" binding:"required"`
	GcCycles       uint32 `json:"gcCycles" binding:"required"`
}
