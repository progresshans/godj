//go:build darwin || linux

// Package testprocess provides process ownership checks for external product tests.
// Command timeouts, output policy and assertions remain with each consumer.
package testprocess

import (
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func Wait(waited <-chan error, timeout time.Duration) error {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case err := <-waited:
		return err
	case <-timer.C:
		return errors.New("process Wait remained blocked after forced cleanup")
	}
}

func OwnedGroups(rootPID int) ([]int, error) {
	output, err := exec.Command("ps", "-Ao", "pid=,ppid=,pgid=").Output()
	if err != nil {
		return []int{rootPID}, fmt.Errorf("inspect process tree: %w", err)
	}
	return parseOwnedGroups(output, rootPID, syscall.Getpgrp())
}

func parseOwnedGroups(output []byte, rootPID, ownGroup int) ([]int, error) {
	type process struct{ pid, ppid, pgid int }
	var processes []process
	for _, line := range strings.Split(string(output), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if len(fields) != 3 {
			return []int{rootPID}, errors.New("inspect process tree: invalid ps row")
		}
		pid, pidErr := strconv.Atoi(fields[0])
		ppid, ppidErr := strconv.Atoi(fields[1])
		pgid, pgidErr := strconv.Atoi(fields[2])
		if errors.Join(pidErr, ppidErr, pgidErr) != nil {
			return []int{rootPID}, errors.New("inspect process tree: invalid identifier")
		}
		processes = append(processes, process{pid: pid, ppid: ppid, pgid: pgid})
	}
	descendants := map[int]struct{}{rootPID: {}}
	for changed := true; changed; {
		changed = false
		for _, candidate := range processes {
			if _, owned := descendants[candidate.ppid]; !owned {
				continue
			}
			if _, exists := descendants[candidate.pid]; exists {
				continue
			}
			descendants[candidate.pid] = struct{}{}
			changed = true
		}
	}
	groups := map[int]struct{}{rootPID: {}}
	for _, candidate := range processes {
		if _, owned := descendants[candidate.pid]; owned {
			groups[candidate.pgid] = struct{}{}
		}
	}
	result := make([]int, 0, len(groups))
	for group := range groups {
		if group <= 1 || group == ownGroup {
			return []int{rootPID}, errors.New("inspect process tree: unsafe process group")
		}
		result = append(result, group)
	}
	sort.Ints(result)
	return result, nil
}

func SignalGroup(group int, signal syscall.Signal) error {
	if group <= 1 || group == syscall.Getpgrp() {
		return errors.New("refuse to signal unsafe process group")
	}
	err := syscall.Kill(-group, signal)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}

func KillGroups(groups []int, root int) error {
	var result error
	for index := len(groups) - 1; index >= 0; index-- {
		if groups[index] == root {
			continue
		}
		result = errors.Join(result, SignalGroup(groups[index], syscall.SIGKILL))
	}
	return errors.Join(result, SignalGroup(root, syscall.SIGKILL))
}

func WaitAbsent(groups []int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		var remaining []int
		for _, group := range groups {
			err := syscall.Kill(-group, 0)
			if err == nil || errors.Is(err, syscall.EPERM) {
				remaining = append(remaining, group)
			}
		}
		if len(remaining) == 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("process groups remain: %v", remaining)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func MergeGroups(left, right []int) []int {
	unique := make(map[int]struct{}, len(left)+len(right))
	for _, group := range append(append([]int(nil), left...), right...) {
		unique[group] = struct{}{}
	}
	result := make([]int, 0, len(unique))
	for group := range unique {
		result = append(result, group)
	}
	sort.Ints(result)
	return result
}
