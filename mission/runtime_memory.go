package mission

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"

	"squadron/aitools"
	"squadron/config"
	"squadron/internal/paths"
	missionruntime "squadron/mission/runtime"
)

// runtimeAttemptTools materializes only the storage slots declared for this
// task. The scratchpad is stable for one attempt across process restarts, but
// never shared with another task or retry attempt.
func (r *Runner) runtimeAttemptTools(_ context.Context, dispatch missionruntime.Dispatch) (map[string]aitools.Tool, error) {
	taskName := dispatch.TaskName
	if dispatch.Grant.Iteration != nil {
		taskName = dispatch.Grant.Iteration.ParentTask
	}
	task := r.mission.GetTaskByName(taskName)
	if task == nil {
		return nil, fmt.Errorf("task %q is not declared", taskName)
	}
	packetNames := make(map[string]struct{}, len(r.mission.Packets)+len(task.Packets))
	for _, name := range r.mission.Packets {
		packetNames[name] = struct{}{}
	}
	for _, name := range task.Packets {
		packetNames[name] = struct{}{}
	}
	hasFileInput := false
	for _, input := range r.mission.Inputs {
		if input.Type == config.InputTypeFile {
			hasFileInput = true
			break
		}
	}
	if !r.mission.Scratchpad && len(packetNames) == 0 && !hasFileInput {
		return nil, nil
	}
	store := &missionMemoryStore{slots: make(map[string]*memorySlot)}
	if r.mission.Scratchpad {
		if dispatch.RunID == "" || dispatch.AttemptID == "" {
			return nil, fmt.Errorf("run and attempt IDs are required for scratchpad storage")
		}
		runRoot, err := MissionScratchpadPath(r.mission.Name, dispatch.RunID)
		if err != nil {
			return nil, err
		}
		if err := os.MkdirAll(runRoot, 0700); err != nil {
			return nil, fmt.Errorf("create run scratchpad root: %w", err)
		}
		if err := writeRunMetadata(runRoot, r.mission.Name, dispatch.RunID, config.ScratchpadCleanupDays); err != nil {
			return nil, fmt.Errorf("record run scratchpad retention: %w", err)
		}
		attemptHash := sha256.Sum256([]byte(dispatch.AttemptID))
		attemptRoot := filepath.Join(runRoot, "attempts", hex.EncodeToString(attemptHash[:]))
		if err := os.MkdirAll(attemptRoot, 0700); err != nil {
			return nil, fmt.Errorf("create attempt scratchpad: %w", err)
		}
		store.slots[config.ScratchpadSlotName] = &memorySlot{absPath: attemptRoot}
	}
	for _, packet := range r.cfg.Packets {
		if _, allowed := packetNames[packet.Name]; !allowed {
			continue
		}
		packetRoot, err := paths.ResolveFolderPath(packet.Path)
		if err != nil {
			return nil, fmt.Errorf("resolve packet %q: %w", packet.Name, err)
		}
		store.slots[config.PacketSlotPrefix+packet.Name] = &memorySlot{absPath: packetRoot, description: packet.Description}
		delete(packetNames, packet.Name)
	}
	for name := range packetNames {
		return nil, fmt.Errorf("packet %q is not configured", name)
	}
	for _, input := range r.mission.Inputs {
		if input.Type != config.InputTypeFile {
			continue
		}
		root, ok := r.runtimeFileInputDirs[input.Name]
		if !ok {
			return nil, fmt.Errorf("file input %q is not staged", input.Name)
		}
		store.slots[config.InputFileSlotPrefix+input.Name] = &memorySlot{absPath: root, description: input.Description}
	}
	tools := map[string]aitools.Tool{
		"file_list":   &aitools.MemoryListTool{Store: store},
		"file_read":   &aitools.MemoryReadTool{Store: store},
		"file_view":   &aitools.FileViewTool{Store: store},
		"file_search": &aitools.MemorySearchTool{Store: store},
		"file_grep":   &aitools.MemoryGrepTool{Store: store},
	}
	if r.mission.Scratchpad {
		tools["file_create"] = &aitools.MemoryCreateTool{Store: store}
		tools["file_delete"] = &aitools.MemoryDeleteTool{Store: store}
	}
	return tools, nil
}
