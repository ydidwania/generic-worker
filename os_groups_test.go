package main

import (
	"fmt"
	"io/ioutil"
	"path/filepath"
	"strings"
	"testing"
)

func TestMissingScopesOSGroups(t *testing.T) {
	setup(t, "TestMissingScopesOSGroups")
	defer teardown(t)
	payload := GenericWorkerPayload{
		Command:    helloGoodbye(),
		MaxRunTime: 1,
		OSGroups:   []string{"abc", "def"},
	}
	td := testTask(t)
	// don't set any scopes
	taskID := scheduleAndExecute(t, td, payload)

	ensureResolution(t, taskID, "exception", "malformed-payload")

	// check log mentions both missing scopes
	bytes, err := ioutil.ReadFile(filepath.Join(taskContext.TaskDir, logPath))
	if err != nil {
		t.Fatalf("Error when trying to read log file: %v", err)
	}
	logtext := string(bytes)
	if !strings.Contains(logtext, "generic-worker:os-group:abc") || !strings.Contains(logtext, "generic-worker:os-group:def") {
		t.Fatalf("Was expecting log file to contain missing scopes, but it doesn't")
	}
}

func TestOSGroupsRespected(t *testing.T) {
	setup(t, "TestOSGroupsRespected")
	defer teardown(t)
	payload := GenericWorkerPayload{
		Command:    helloGoodbye(),
		MaxRunTime: 30,
		OSGroups:   []string{"abc", "def"},
	}
	td := testTask(t)
	td.Scopes = []string{"generic-worker:os-group:abc", "generic-worker:os-group:def"}
	taskID := scheduleAndExecute(t, td, payload)

	if config.RunTasksAsCurrentUser {
		ensureResolution(t, taskID, "completed", "completed")

		// check log mentions both missing scopes
		bytes, err := ioutil.ReadFile(filepath.Join(taskContext.TaskDir, logPath))
		if err != nil {
			t.Fatalf("Error when trying to read log file: %v", err)
		}
		logtext := string(bytes)
		substring := fmt.Sprintf("Not adding user to groups %v since we are running as current user.", payload.OSGroups)
		if !strings.Contains(logtext, substring) {
			t.Log(logtext)
			t.Fatalf("Was expecting log to contain string %v.", substring)
		}
	} else {
		// check task had malformed payload, due to non existent groups
		ensureResolution(t, taskID, "exception", "malformed-payload")

		// check log mentions both missing scopes
		bytes, err := ioutil.ReadFile(filepath.Join(taskContext.TaskDir, logPath))
		if err != nil {
			t.Fatalf("Error when trying to read log file: %v", err)
		}
		logtext := string(bytes)
		substring := fmt.Sprintf("Could not add os group(s) to task user: %v", payload.OSGroups)
		if !strings.Contains(logtext, substring) {
			t.Log(logtext)
			t.Fatalf("Was expecting log to contain string %v.", substring)
		}
	}
}
