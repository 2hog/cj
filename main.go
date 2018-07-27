package main

import (
	"fmt"
	"io"
	"io/ioutil"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"math/rand"

	flag "github.com/spf13/pflag"

	"github.com/docker/cli/cli/compose/convert"
	"github.com/docker/cli/cli/compose/loader"
	composetypes "github.com/docker/cli/cli/compose/types"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/swarm"
	apiclient "github.com/docker/docker/client"
	"github.com/pkg/errors"
	"golang.org/x/net/context"
)

var letters = []rune("abcdefghijklmnopqrstuvwxyz")

func randSeq(n int) string {
	b := make([]rune, n)
	for i := range b {
		b[i] = letters[rand.Intn(len(letters))]
	}
	return string(b)
}

func main() {

	// parse flags
	var serviceName string
	var composeFile string
	var stackName string
	var global bool

	flag.StringVarP(&serviceName, "service", "s", "", "set the service spec to use for the job")
	flag.StringVarP(&composeFile, "file", "f", "docker-compose.yml", "set the compose file to read the specs from")
	flag.StringVarP(&stackName, "stack", "S", "", "set the stack to use for the job")
	flag.BoolVarP(&global, "global", "g", false, "run the job in all nodes")
	flag.Parse()
	args := flag.Args()

	if len(flag.Args()) == 0 {
		flag.Usage()
		return
	}

	if len(serviceName) == 0 {
		fmt.Printf("Service is needed, please use the --service flag\n")
		os.Exit(42)
	}
	if _, err := os.Stat(composeFile); os.IsNotExist(err) {
		fmt.Printf("Compose file \"%s\" does not exist, please use the --file flag to set one that exists\n", composeFile)
		os.Exit(42)
	}
	if len(stackName) == 0 {
		fmt.Printf("Stack is needed, please use the --stack flag\n")
		os.Exit(42)
	}

	// run cj
	exitCode, err := cj(composeFile, stackName, serviceName, args, global)
	if err != nil {
		fmt.Printf("Could not run job %s\n", err)
	}

	os.Exit(exitCode)
}

func cj(composeFile string, stackName string, serviceName string, args []string, global bool) (int, error) {
	// initialize the client
	cli, err := apiclient.NewEnvClient()
	if err != nil {
		return 1, err
	}
	ctx := context.Background()

	// get the config
	configDetails, err := getConfigDetails(composeFile)
	if err != nil {
		return 2, err
	}
	config, err := loader.Load(configDetails)
	if err != nil {
		return 3, err
	}

	// check if connected in a Swarm manager
	if err := checkDaemonIsSwarmManager(ctx, cli); err != nil {
		return 4, err
	}

	// get the defined services
	namespace := convert.NewNamespace(stackName)
	services, err := convert.Services(namespace, config, cli)
	if err != nil {
		return 5, err
	}

	// find the given service and run the job
	for _, service := range services {
		if service.Name == namespace.Name()+"_"+serviceName {
			return runJob(ctx, cli, service, args, global)
		}
	}

	return 0, errors.Errorf("service \"%s\" was not found in the compose file", serviceName)
}

func getConfigDetails(composefile string) (composetypes.ConfigDetails, error) {
	var details composetypes.ConfigDetails

	if composefile == "-" {
		workingDir, err := os.Getwd()
		if err != nil {
			return details, err
		}
		details.WorkingDir = workingDir
	} else {
		absPath, err := filepath.Abs(composefile)
		if err != nil {
			return details, err
		}
		details.WorkingDir = filepath.Dir(absPath)
	}

	configFile, err := getConfigFile(composefile)
	if err != nil {
		return details, err
	}
	// TODO: support multiple files
	details.ConfigFiles = []composetypes.ConfigFile{*configFile}
	details.Environment, err = buildEnvironment(os.Environ())
	return details, err
}

func buildEnvironment(env []string) (map[string]string, error) {
	result := make(map[string]string, len(env))
	for _, s := range env {
		// if value is empty, s is like "K=", not "K".
		if !strings.Contains(s, "=") {
			return result, errors.Errorf("unexpected environment %q", s)
		}
		kv := strings.SplitN(s, "=", 2)
		result[kv[0]] = kv[1]
	}
	return result, nil
}

func getConfigFile(filename string) (*composetypes.ConfigFile, error) {
	var bytes []byte
	var err error

	bytes, err = ioutil.ReadFile(filename)
	if err != nil {
		return nil, err
	}

	config, err := loader.ParseYAML(bytes)
	if err != nil {
		return nil, err
	}

	return &composetypes.ConfigFile{
		Filename: filename,
		Config:   config,
	}, nil
}

// checkDaemonIsSwarmManager does an Info API call to verify that the daemon is
// a swarm manager. This is necessary because we must create networks before we
// create services, but the API call for creating a network does not return a
// proper status code when it can't create a network in the "global" scope.
func checkDaemonIsSwarmManager(ctx context.Context, cli apiclient.CommonAPIClient) error {
	info, err := cli.Info(ctx)
	if err != nil {
		return err
	}
	if !info.Swarm.ControlAvailable {
		return errors.New("this node is not a swarm manager. Use \"docker swarm init\" or \"docker swarm join\" to connect this node to swarm and try again")
	}
	return nil
}

func runJob(ctx context.Context, cli apiclient.CommonAPIClient, service swarm.ServiceSpec, cmd []string, global bool) (int, error) {

	// set the command
	service.TaskTemplate.ContainerSpec.Command = cmd
	service.TaskTemplate.ContainerSpec.Args = make([]string, 0)

	// set the restart policy
	service.TaskTemplate.RestartPolicy = &swarm.RestartPolicy{
		Condition: swarm.RestartPolicyConditionNone,
	}

	// update the name of the service
	service.Name = service.Name + "_cj-job_" + randSeq(4)

	// set the service mode
	serviceMode := swarm.ServiceMode{}
	if global {
		serviceMode.Global = &swarm.GlobalService{}
	} else {
		replicas := uint64(1)
		serviceMode.Replicated = &swarm.ReplicatedService{Replicas: &replicas}
	}
	service.Mode = serviceMode

	// run the service
	serviceResponse, err := cli.ServiceCreate(ctx, service, types.ServiceCreateOptions{
		QueryRegistry: true,
	})
	if err != nil {
		return 0, err
	}
	defer cli.ServiceRemove(ctx, serviceResponse.ID)

	taskFilters := filters.NewArgs()
	taskFilters.Add("service", serviceResponse.ID)
	c := make(chan int)
	go waitOnTasks(ctx, cli, taskFilters, c)
	logs, err := copyLogs(ctx, cli, serviceResponse.ID, os.Stdout, c)

	exitCode := <-c
	if err != nil {
		logs.Close()
	}

	return exitCode, nil
}

func waitOnTasks(ctx context.Context, cli apiclient.CommonAPIClient, taskFilters filters.Args, c chan int) {
	exitCode := 0

	for {
		finished := true
		tasks, err := cli.TaskList(ctx, types.TaskListOptions{Filters: taskFilters})
		if err != nil {
			continue
		}
		for _, task := range tasks {
			if task.Status.State != swarm.TaskStateComplete && task.Status.State != swarm.TaskStateFailed && task.Status.State != swarm.TaskStateRejected {
				finished = false
				break
			}

			if task.Status.ContainerStatus.ExitCode != 0 {
				if math.Abs(float64(exitCode)) < math.Abs(float64(task.Status.ContainerStatus.ExitCode)) {
					exitCode = task.Status.ContainerStatus.ExitCode
				}
			}
		}

		if finished {
			break
		}

		time.Sleep(1 * time.Second)
	}

	c <- exitCode
}

func copyLogs(ctx context.Context, cli apiclient.CommonAPIClient, serviceID string, writer io.WriteCloser, c chan int) (io.ReadCloser, error) {
	r, err := cli.ServiceLogs(ctx, serviceID, types.ContainerLogsOptions{Follow: true, ShowStderr: true, ShowStdout: true})
	if err != nil {
		return nil, err
	}

	go io.Copy(writer, r)
	return r, nil
}
