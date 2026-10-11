// The live suite uses the compiled CLI and existing credentials. It never starts a task.
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

var binary string

func require(ok bool, message string) {
	if !ok {
		fmt.Fprintln(os.Stderr, "Live check failed: "+message)
		os.Exit(1)
	}
}

func call(args ...string) ([]byte, string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, append([]string{"--no-input", "--timeout", "15s", "--dry-run", "--json"}, args...)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.Bytes(), stderr.String(), err
}

func read(args ...string) map[string]json.RawMessage {
	raw, _, err := call(args...)
	require(err == nil, "CLI failed for "+strings.Join(args, " ")+"; check service access and saved credentials locally")
	var result map[string]json.RawMessage
	require(json.Unmarshal(raw, &result) == nil, "invalid JSON object for "+strings.Join(args, " "))
	return result
}

func object(raw json.RawMessage) map[string]json.RawMessage {
	var list []json.RawMessage
	if len(raw) > 0 && raw[0] == '[' {
		require(json.Unmarshal(raw, &list) == nil && len(list) == 1, "expected one device object")
		raw = list[0]
	}
	var result map[string]json.RawMessage
	require(json.Unmarshal(raw, &result) == nil && result != nil, "missing device object")
	return result
}

func integer(raw json.RawMessage) int64 {
	var value int64
	require(json.Unmarshal(raw, &value) == nil, "expected integer from device")
	return value
}

func main() {
	path := "bin/roborocli"
	if len(os.Args) > 1 {
		path = os.Args[1]
	}
	var err error
	binary, err = filepath.Abs(path)
	require(err == nil, "cannot resolve CLI path")
	for _, transport := range []string{"default", "mqtt"} {
		args := []string{"command", "get_status"}
		if transport == "mqtt" {
			args = append([]string{"--mqtt"}, args...)
		}
		report := read(args...)
		require(string(report["method"]) == `"get_status"`, "missing RPC receipt")
		require(string(report["transport"]) == `"local"` || string(report["transport"]) == `"mqtt"`, "status did not come from the device")
		if transport == "mqtt" {
			require(string(report["transport"]) == `"mqtt"`, "forced MQTT did not use MQTT")
		}
		status := object(report["result"])
		battery := integer(status["battery"])
		require(battery >= 0 && battery <= 100, "invalid battery level")
		integer(status["state"])
		fmt.Println("PASS live status over " + transport)
	}
	// Older firmware rejects this getter. A rejection must never become a success receipt.
	raw, stderr, err := call("command", "get_airdry_hours")
	if err != nil {
		require(strings.Contains(stderr, "device rejected command: unknown_method"), "unsupported getter failed for an unrelated reason")
	} else {
		result := object(raw)
		require(string(result["result"]) != `"unknown_method"`, "unsupported method returned success")
	}
	caps := object(read("capabilities")["capabilities"])
	require(len(caps["model"]) > 2, "capabilities have no model")
	var modes []string
	require(json.Unmarshal(caps["modes"], &modes) == nil && len(modes) > 0, "capabilities have no modes")
	var records []map[string]json.RawMessage
	require(json.Unmarshal(read("history", "records", "--limit", "3")["records"], &records) == nil, "invalid cleaning records")
	var prior int64
	for i, record := range records {
		id := integer(record["id"])
		require(id > 0, "invalid cleaning record ID")
		if i > 0 {
			require(id <= prior, "history is not newest first")
		}
		prior = id
		var parts []map[string]json.RawMessage
		require(json.Unmarshal(record["parts"], &parts) == nil && len(parts) > 0, "record has no device evidence")
		last := parts[len(parts)-1]
		if string(last["complete"]) == "0" {
			require(string(record["outcome"]) != `"completed"`, "incomplete run reported as completed")
		}
		if string(last["finish_reason"]) == "60" {
			require(string(record["outcome"]) == `"interrupted"`, "manual interruption was not decoded")
		}
	}
	floor := object(read("maps", "show")["map"])
	width, height := integer(floor["width"]), integer(floor["height"])
	require(width > 0 && height > 0, "empty live map")
	rooms := object(floor["rooms"])
	require(len(rooms) > 0, "live map has no rooms")
	for _, raw := range rooms {
		room := object(raw)
		var bounds []int
		require(json.Unmarshal(room["bounds_mm"], &bounds) == nil && len(bounds) == 4 && bounds[2] > bounds[0] && bounds[3] > bounds[1], "invalid room bounds")
	}
	pngBytes, _, err := call("maps", "render", "--output", "-")
	require(err == nil, "map render failed")
	image, err := png.Decode(bytes.NewReader(pngBytes))
	require(err == nil, "renderer did not return a valid PNG")
	require(image.Bounds().Dx() > 0 && image.Bounds().Dy() > 0, "empty PNG")
	routines, _, err := call("routines", "list")
	require(err == nil, "routine list failed")
	var routineList []json.RawMessage
	require(json.Unmarshal(routines, &routineList) == nil, "invalid routine list")
	dock := read("dock", "status")
	require(len(dock["status"]) > 0, "dock status has no live state")
	stream, _, err := call("watch", "--count", "3", "--interval", "1s")
	require(err == nil, "watch failed")
	scanner := bufio.NewScanner(bytes.NewReader(stream))
	events := 0
	for scanner.Scan() {
		event := object(scanner.Bytes())
		require(len(event["time"]) > 0 && len(event["kind"]) > 0 && len(event["transport"]) > 0, "invalid stream event")
		events++
	}
	require(scanner.Err() == nil && events == 3, "watch did not stop after three events")
	for _, args := range [][]string{{"control", "mode", "vacuum"}, {"dock", "wash-start"}, {"dock", "dry-start"}, {"dock", "empty-start"}, {"dock", "auto-dry", "off"}, {"maps", "use", "0"}, {"routines", "run", "0"}, {"datapoints", "set", "200", "1"}, {"maps", "render", "--output", "refused.png"}} {
		_, stderr, err := call(args...)
		require(err != nil && strings.Contains(stderr, "dry-run: refusing"), "dry-run did not refuse "+strings.Join(args, " "))
	}
	fmt.Println("PASS history, capabilities, map geometry and PNG, routines, dock reads, stream, and mutation guards")
	fmt.Println("NOT CHECKED: physical controls and A01/B01 hardware")
}
