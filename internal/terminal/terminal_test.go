package terminal

import (
	"bytes"
	"io"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestFramesRoundTripDataAndResizes(t *testing.T) {
	var buf bytes.Buffer
	big := bytes.Repeat([]byte("x"), maxFrame+10)
	WriteData(&buf, []byte("ls\r"))
	WriteResize(&buf, 120, 40)
	WriteData(&buf, big)
	var data []byte
	var sizes [][2]int
	err := ReadFrames(&buf, func(b []byte) error { data = append(data, b...); return nil }, func(c, r int) { sizes = append(sizes, [2]int{c, r}) })
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "ls\r"+string(big) {
		t.Fatalf("data corrupted: %d bytes", len(data))
	}
	if len(sizes) != 1 || sizes[0] != [2]int{120, 40} {
		t.Fatalf("resizes = %v", sizes)
	}
}

func TestReadFramesRejectsGarbage(t *testing.T) {
	for name, in := range map[string][]byte{
		"unknown type":      {9, 0, 1, 'x'},
		"short resize":      {FrameResize, 0, 2, 0, 1},
		"truncated payload": {FrameData, 0, 10, 'a'},
	} {
		err := ReadFrames(bytes.NewReader(in), func([]byte) error { return nil }, func(int, int) {})
		if err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestStartGivesTheProgramATerminalOfTheRequestedSize(t *testing.T) {
	cmd := exec.Command("sh", "-c", "stty size; tty >/dev/null && echo is-a-tty; read line; echo got:$line")
	master, err := Start(cmd, 91, 27)
	if err != nil {
		t.Fatal(err)
	}
	defer master.Close()
	master.Write([]byte("hello\n"))
	done := make(chan []byte)
	go func() {
		out, _ := io.ReadAll(master)
		done <- out
	}()
	cmd.Wait()
	var out []byte
	select {
	case out = <-done:
	case <-time.After(5 * time.Second):
		master.Close()
		out = <-done
	}
	s := strings.ReplaceAll(string(out), "\r", "")
	for _, want := range []string{"27 91", "is-a-tty", "got:hello"} {
		if !strings.Contains(s, want) {
			t.Errorf("output %q missing %q", s, want)
		}
	}
}
