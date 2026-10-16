// Package blackbox contains the process and wire probes used by conformance
// tests. It deliberately observes only command output, sockets, and HTTP;
// tests must not reach into server implementation packages.
package blackbox

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Runner launches one built executable for black-box assertions.
type Runner struct {
	Executable string
	Dir        string
	Env        []string
}

// Run executes a command and captures both public output streams.
func (r Runner) Run(ctx context.Context, args ...string) Result {
	return r.run(ctx, "", args...)
}

// RunWithStdin executes a command with secret input supplied through stdin.
func (r Runner) RunWithStdin(ctx context.Context, input string, args ...string) Result {
	return r.run(ctx, input, args...)
}

func (r Runner) run(ctx context.Context, input string, args ...string) Result {
	command := exec.CommandContext(ctx, r.Executable, args...)
	command.Dir = r.Dir
	command.Env = append(os.Environ(), r.Env...)
	if input != "" {
		command.Stdin = strings.NewReader(input)
	}
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	return Result{Stdout: stdout.String(), Stderr: stderr.String(), ExitCode: exitCode(command, err), Err: err}
}

// Result is the complete public result of one command invocation.
type Result struct {
	Stdout   string
	Stderr   string
	ExitCode int
	Err      error
}

func exitCode(command *exec.Cmd, err error) int {
	if err == nil || command.ProcessState == nil {
		return 0
	}
	if status, ok := command.ProcessState.Sys().(syscall.WaitStatus); ok && status.Signaled() {
		return 128 + int(status.Signal())
	}
	return command.ProcessState.ExitCode()
}

// Process is a running executable with line-oriented stdout observation.
type Process struct {
	control processControl
	output  processOutput
}

type processControl struct {
	command *exec.Cmd
	read    *sync.WaitGroup
}

type processOutput struct {
	events <-chan string
	mu     sync.Mutex
	stdout bytes.Buffer
	stderr bytes.Buffer
}

// Start launches an executable without waiting for it to exit.
func (r Runner) Start(ctx context.Context, args ...string) (*Process, error) {
	command := exec.CommandContext(ctx, r.Executable, args...)
	command.Dir = r.Dir
	command.Env = append(os.Environ(), r.Env...)
	stdout, err := command.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := command.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := command.Start(); err != nil {
		return nil, err
	}
	lines := make(chan string, 16)
	read := new(sync.WaitGroup)
	read.Add(2)
	process := &Process{control: processControl{command: command, read: read}, output: processOutput{events: lines}}
	go func() {
		defer read.Done()
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			line := scanner.Text()
			process.output.appendStdout(line)
			lines <- line
		}
		close(lines)
	}()
	go func() {
		defer read.Done()
		_, _ = io.Copy(stderrWriter{output: &process.output}, stderr)
	}()
	return process, nil
}

// NextJSONEvent waits for and decodes the next JSON line from stdout.
func (p *Process) NextJSONEvent(ctx context.Context, target any) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case line, ok := <-p.output.events:
		if !ok {
			return io.EOF
		}
		if err := json.Unmarshal([]byte(line), target); err != nil {
			return fmt.Errorf("decode process event %q: %w", line, err)
		}
		return nil
	}
}

// Wait waits for process exit and returns its observed result.
func (p *Process) Wait() Result {
	err := p.control.command.Wait()
	p.control.read.Wait()
	return p.output.result(p.control.command, err)
}

func (output *processOutput) result(command *exec.Cmd, err error) Result {
	output.mu.Lock()
	defer output.mu.Unlock()
	return Result{Stdout: output.stdout.String(), Stderr: output.stderr.String(), ExitCode: exitCode(command, err), Err: err}
}

// Stop requests the same graceful signal used by an operator stop command.
func (p *Process) Stop() error {
	return p.control.command.Process.Signal(syscall.SIGTERM)
}

// Crash terminates the process without giving it a graceful shutdown path.
func (p *Process) Crash() error {
	return p.control.command.Process.Kill()
}

func (output *processOutput) appendStdout(line string) {
	output.mu.Lock()
	defer output.mu.Unlock()
	output.stdout.WriteString(line)
	output.stdout.WriteByte('\n')
}

// stderrWriter appends standard error as it arrives so a test can observe
// records written before the process exits.
type stderrWriter struct{ output *processOutput }

func (writer stderrWriter) Write(data []byte) (int, error) {
	writer.output.mu.Lock()
	defer writer.output.mu.Unlock()
	return writer.output.stderr.Write(data)
}

// Snapshot returns the standard output and standard error observed so far,
// while the process may still be running.
func (p *Process) Snapshot() (stdout, stderr string) {
	p.output.mu.Lock()
	defer p.output.mu.Unlock()
	return p.output.stdout.String(), p.output.stderr.String()
}

// InstanceOptions selects how StartInstance launches serve. Both listeners
// bind port 0 on loopback, so the operating system selects free ports and the
// ready event reports them.
type InstanceOptions struct {
	DataDirectory string
	// Diagnostics enables the diagnostics listener.
	Diagnostics bool
	// Arguments are extra serve arguments, such as TLS files or limits.
	Arguments []string
	// ReadyTimeout bounds the wait for the ready event. Zero selects 5 seconds.
	ReadyTimeout time.Duration
}

// Instance is a serve process that has reported ready. The addresses come
// from the ready event, not from the test.
type Instance struct {
	Process            *Process
	MySQLAddress       string
	DiagnosticsAddress string
	Ready              map[string]any
}

// Initialize creates a data directory through the operator init command.
func Initialize(ctx context.Context, runner Runner, directory, password string) error {
	result := runner.RunWithStdin(ctx, password+"\n", "init", directory, "--password-stdin", "--format=json")
	if result.ExitCode != 0 {
		return fmt.Errorf("initialize instance: exit %d: %s%s", result.ExitCode, strings.TrimSpace(result.Stdout), strings.TrimSpace(result.Stderr))
	}
	return nil
}

// StartInstance launches serve with JSON lifecycle output and waits for its
// ready event. As with Runner.Start, ctx bounds the life of the process, not
// only the wait. When serve stops before it is ready, the error contains the
// process output.
func StartInstance(ctx context.Context, runner Runner, options InstanceOptions) (*Instance, error) {
	arguments := []string{"serve", "--format=json", "--data-directory=" + options.DataDirectory, "--mysql-listen-address=127.0.0.1:0"}
	if options.Diagnostics {
		arguments = append(arguments, "--diagnostics-listen-address=127.0.0.1:0")
	}
	process, err := runner.Start(ctx, append(arguments, options.Arguments...)...)
	if err != nil {
		return nil, err
	}
	ready, err := process.awaitReady(ctx, options.ReadyTimeout)
	if err != nil {
		_ = process.Crash()
		result := process.Wait()
		return nil, fmt.Errorf("wait for ready event: %w; stdout=%q stderr=%q", err, result.Stdout, result.Stderr)
	}
	instance := &Instance{Process: process, Ready: ready}
	instance.MySQLAddress, _ = ready["mysql_address"].(string)
	instance.DiagnosticsAddress, _ = ready["diagnostics_address"].(string)
	if instance.MySQLAddress == "" || options.Diagnostics && instance.DiagnosticsAddress == "" {
		_ = process.Crash()
		_ = process.Wait()
		return nil, fmt.Errorf("ready event does not report the bound listener addresses: %#v", ready)
	}
	return instance, nil
}

// defaultReadyTimeout bounds the wait for a ready event when
// InstanceOptions does not set ReadyTimeout.
const defaultReadyTimeout = 5 * time.Second

// awaitReady skips recovery events and returns the ready event. Any other
// state before ready is an error.
func (p *Process) awaitReady(parent context.Context, timeout time.Duration) (map[string]any, error) {
	if timeout == 0 {
		timeout = defaultReadyTimeout
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	for {
		var event map[string]any
		if err := p.NextJSONEvent(ctx, &event); err != nil {
			return nil, err
		}
		switch event["state"] {
		case "recovering":
			continue
		case "ready":
			return event, nil
		default:
			return nil, fmt.Errorf("unexpected event before ready: %#v", event)
		}
	}
}

// Stop requests a graceful stop and waits for the process to exit.
func (i *Instance) Stop() Result {
	_ = i.Process.Stop()
	return i.Process.Wait()
}

// HTTPJSON performs one diagnostics request and decodes its JSON response.
func HTTPJSON(ctx context.Context, address, path string, target any) (int, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+address+path, nil)
	if err != nil {
		return 0, err
	}
	response, err := (&http.Client{Timeout: 5 * time.Second}).Do(request)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	if err := json.NewDecoder(response.Body).Decode(target); err != nil {
		return response.StatusCode, err
	}
	return response.StatusCode, nil
}

// MySQLHandshake is the public subset of a classic-protocol greeting that
// black-box tests need before a full SQL client exists.
type MySQLHandshake struct {
	ProtocolVersion byte
	ServerVersion   string
	ConnectionID    uint32
}

// ProbeMySQL reads either a valid initial handshake or a protocol error packet
// without depending on a Go database driver.
func ProbeMySQL(ctx context.Context, address string) (MySQLHandshake, error) {
	dialer := net.Dialer{Timeout: 5 * time.Second}
	connection, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return MySQLHandshake{}, err
	}
	defer connection.Close()
	packet, err := readPacket(connection)
	if err != nil {
		return MySQLHandshake{}, err
	}
	if len(packet) == 0 {
		return MySQLHandshake{}, fmt.Errorf("empty MySQL packet")
	}
	if packet[0] == 0xff {
		return MySQLHandshake{}, fmt.Errorf("MySQL handshake rejected: %s", mysqlError(packet))
	}
	if packet[0] != 0x0a {
		return MySQLHandshake{}, fmt.Errorf("unsupported MySQL protocol version %d", packet[0])
	}
	versionEnd := bytes.IndexByte(packet[1:], 0)
	if versionEnd < 0 {
		return MySQLHandshake{}, fmt.Errorf("malformed MySQL handshake")
	}
	versionEnd++
	if len(packet) < versionEnd+5 {
		return MySQLHandshake{}, fmt.Errorf("truncated MySQL handshake")
	}
	return MySQLHandshake{
		ProtocolVersion: packet[0],
		ServerVersion:   string(packet[1:versionEnd]),
		ConnectionID:    binary.LittleEndian.Uint32(packet[versionEnd+1 : versionEnd+5]),
	}, nil
}

func readPacket(reader io.Reader) ([]byte, error) {
	header := make([]byte, 4)
	if _, err := io.ReadFull(reader, header); err != nil {
		return nil, err
	}
	length := int(header[0]) | int(header[1])<<8 | int(header[2])<<16
	packet := make([]byte, length)
	if _, err := io.ReadFull(reader, packet); err != nil {
		return nil, err
	}
	return packet, nil
}

func mysqlError(packet []byte) string {
	if len(packet) < 3 {
		return "unknown error"
	}
	return "code=" + strconv.Itoa(int(binary.LittleEndian.Uint16(packet[1:3]))) + " " + strings.TrimSpace(string(packet[3:]))
}
