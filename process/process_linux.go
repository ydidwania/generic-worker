package process

import (
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"golang.org/x/net/context"
)

type Result struct {
	SystemError error
	ExitCode    int64
	Duration    time.Duration
}

type Command struct {
	mutex  sync.RWMutex
	ctx    context.Context
	cli    *client.Client
	resp   container.ContainerCreateCreatedBody
	writer io.Writer
	cmd    []string
}

func (c *Command) DirectOutput(writer io.Writer) {
	c.writer = writer
}

func (c *Command) String() string {
	return fmt.Sprintf("%q", c.cmd)
}

func (c *Command) Execute() (r *Result) {
	r = &Result{}
	c.mutex.Lock()

	r.SystemError = c.cli.ContainerStart(c.ctx, c.resp.ID, types.ContainerStartOptions{})
	if r.SystemError != nil {
		return
	}

	started := time.Now()

	var out io.ReadCloser
	out, r.SystemError = c.cli.ContainerLogs(c.ctx, c.resp.ID, types.ContainerLogsOptions{ShowStdout: true})
	if r.SystemError != nil {
		return
	}
	go io.Copy(c.writer, out)
	res, errch := c.cli.ContainerWait(c.ctx, c.resp.ID, container.WaitConditionNotRunning)
	r.SystemError = <-errch
	if r.SystemError != nil {
		return
	}

	r.ExitCode = (<-res).StatusCode

	finished := time.Now()
	r.Duration = finished.Sub(started)
	return
}

func (r *Result) CrashCause() error {
	return r.SystemError
}

func (r *Result) Crashed() bool {
	return r.SystemError != nil
}

func (r *Result) FailureCause() error {
	return fmt.Errorf("Exit code %v", r.ExitCode)
}

func (r *Result) Failed() bool {
	return r.ExitCode != 0
}

func NewCommand(commandLine []string, workingDirectory string, env []string) (*Command, error) {
	c := &Command{
		ctx:    context.Background(),
		writer: os.Stdout,
		cmd:    commandLine,
	}
	cli, err := client.NewEnvClient()
	if err != nil {
		return nil, err
	}
	c.cli = cli

	cl, err := cli.ImagePull(c.ctx, "ubuntu", types.ImagePullOptions{})
	if err != nil {
		return nil, err
	}
	defer cl.Close()
	c.resp, err = c.cli.ContainerCreate(
		c.ctx,
		&container.Config{
			Image:      "ubuntu",
			Cmd:        commandLine,
			WorkingDir: workingDirectory,
			Env:        env,
		},
		nil,
		nil,
		"",
	)
	if err != nil {
		return nil, err
	}

	return c, nil
}

func (c *Command) Kill() error {
	return nil
}
