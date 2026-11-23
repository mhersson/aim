// The private Claude fixture reports the hooks of one local conversation.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

func main() {
	const session = "12345678-1234-4234-8234-123456789abc"
	transcript := filepath.Join(os.Getenv("HOME"), session+".jsonl")
	cwd, err := os.Getwd()
	check(err)
	hook := func(name string) {
		payload, err := json.Marshal(map[string]string{
			"hook_event_name": name, "session_id": session, "cwd": cwd, "transcript_path": transcript,
		})
		check(err)
		c := exec.Command(os.Getenv("AIM_BIN"), "hook", "claude-code")
		c.Stdin = bytes.NewReader(payload)
		check(c.Run())
	}
	if len(os.Args) == 5 && os.Args[3] == "--resume" && os.Args[4] == session {
		check(os.WriteFile(filepath.Join(os.Getenv("HOME"), "claude-resumed-cwd"), []byte(cwd), 0o600))
		b, err := os.ReadFile(transcript)
		check(err)
		fmt.Printf("continued %s", b)
	} else {
		check(os.WriteFile(transcript, nil, 0o600))
	}
	hook("SessionStart")
	fmt.Println("Claude fixture ready")
	s := bufio.NewScanner(os.Stdin)
	for s.Scan() {
		hook("UserPromptSubmit")
		check(os.WriteFile(transcript, []byte(s.Text()+"\n"), 0o600))
		check(os.WriteFile("claude-edit.txt", []byte(s.Text()+"\n"), 0o600))
		hook("Stop")
		fmt.Println("Claude fixture finished")
	}
	check(s.Err())
}

func check(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
