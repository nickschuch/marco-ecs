package main

import (
	"strconv"
	"strings"
	"sync"

	log "github.com/Sirupsen/logrus"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/ecs"
	"github.com/jasonlvhit/gocron"
	"github.com/nickschuch/marco-lib"
	"gopkg.in/alecthomas/kingpin.v1"
)

const name = "ecs"

var (
	// Marco configuration.
	cliMarco = kingpin.Flag("marco", "The remote Marco backend.").Default("http://localhost:81").OverrideDefaultFromEnvar("MARCO_URL").String()

	// AWS Specific configuration.
	cliRegion  = kingpin.Flag("region", "The region to run the build in.").Default("ap-southeast-2").OverrideDefaultFromEnvar("ECS_REGION").String()
	cliCluster = kingpin.Flag("cluster", "The cluster to run this build against.").Default("default").OverrideDefaultFromEnvar("ECS_CLUSTER").String()
	cliPorts   = kingpin.Flag("ports", "The ports you wish to proxy.").Default("80,8080,2368,8983").OverrideDefaultFromEnvar("ECS_PORTS").String()

	// Global clients.
	clientECS *ecs.ECS
	clientEC2 *ec2.EC2
)

func main() {
	kingpin.Parse()

	clientECS = ecs.New(&aws.Config{Region: aws.String(*cliRegion)})
	clientEC2 = ec2.New(&aws.Config{Region: aws.String(*cliRegion)})

	wg := &sync.WaitGroup{}

	// This is a scheduled set of tasks which will unmount old directories which
	// are not being used by container instances.
	wg.Add(1)
	go func() {
		gocron.Every(15).Seconds().Do(Push, *cliMarco)
		go gocron.Start()
	}()

	wg.Wait()
}

func Push(m string) {
	var b []marco.Backend

	// Get a list of backends keyed by the domain.
	list, err := getList()
	if err != nil {
		log.WithFields(log.Fields{
			"type": "push",
		}).Info(err)
		return
	}

	// Convert into the objects required for a push to Marco.
	for d, l := range list {
		n := marco.Backend{
			Type:   name,
			Domain: d,
			List:   l,
		}
		b = append(b, n)
	}

	// Attempt to send data to Marco.
	err = marco.Send(b, *cliMarco)
	if err != nil {
		log.WithFields(log.Fields{
			"type": "push",
		}).Info(err)
		return
	}

	log.WithFields(log.Fields{
		"type": "push",
	}).Info("Successfully pushed data to Marco.")
}

func getList() (map[string][]string, error) {
	list := make(map[string][]string)
	ips := make(map[string]string)

	tasksInput := &ecs.ListTasksInput{
		Cluster: aws.String(*cliCluster),
	}
	tasks, err := clientECS.ListTasks(tasksInput)
	if err != nil {
		return list, err
	}

	// We only have one task that we care about. So we are
	// only going to pass this one in the list.
	describeInput := &ecs.DescribeTasksInput{
		Cluster: aws.String(*cliCluster),
		Tasks:   tasks.TaskArns,
	}
	described, err := clientECS.DescribeTasks(describeInput)
	if err != nil {
		return list, err
	}

	// Get the IP address of each of the container instances.
	// That way we can use these addresses further down on our container urls.
	instancesInput := &ecs.ListContainerInstancesInput{
		Cluster: aws.String(*cliCluster),
	}
	instances, err := clientECS.ListContainerInstances(instancesInput)
	if err != nil {
		return list, err
	}

	for _, i := range instances.ContainerInstanceArns {
		containerInstance := getContainerInstance(i)
		ips[*i] = getEc2IP(containerInstance.Ec2InstanceId)
	}

	// Loop over the containers and build a list of urls to hit.
	for _, t := range described.Tasks {
		for _, c := range t.Containers {
			// Ensure this container has the required environment variable to be
			// exposed through the load balancer.
			domain := getContainerEnv(*t.TaskDefinitionArn, *c.Name, "DOMAIN")
			if domain == "" {
				continue
			}

			// Loop over all the ports that have been exposed.
			for _, p := range c.NetworkBindings {
				// Check that this container has exposed the port that we require.
				containerPort := strconv.FormatInt(*p.ContainerPort, 10)
				if !strings.Contains(*cliPorts, containerPort) {
					continue
				}

				// Add the port to the list.
				hostIP := ips[*t.ContainerInstanceArn]
				hostPort := strconv.FormatInt(*p.HostPort, 10)
				url := "http://" + hostIP + ":" + hostPort
				list[domain] = append(list[domain], url)
			}
		}
	}

	return list, nil
}

func getContainerEnv(definition string, name string, key string) string {
	tasksDefInput := &ecs.DescribeTaskDefinitionInput{
		TaskDefinition: aws.String(definition),
	}
	tasksDefOutput, err := clientECS.DescribeTaskDefinition(tasksDefInput)
	if err != nil {
		log.Info(err)
		return ""
	}

	for _, c := range tasksDefOutput.TaskDefinition.ContainerDefinitions {
		if *c.Name != name {
			continue
		}

		// Now we know we can look for the environment variable.
		for _, e := range c.Environment {
			if *e.Name == key {
				return *e.Value
			}
		}
	}

	return ""
}

func getContainerInstance(arn *string) *ecs.ContainerInstance {
	params := &ecs.DescribeContainerInstancesInput{
		ContainerInstances: []*string{
			aws.String(*arn),
		},
		Cluster: aws.String(*cliCluster),
	}
	resp, err := clientECS.DescribeContainerInstances(params)
	if err != nil {
		log.Info(err)
		return nil
	}

	return resp.ContainerInstances[0]
}

func getEc2IP(id *string) string {
	// Query the EC2 backend for the host that we require.
	params := &ec2.DescribeInstancesInput{
		InstanceIds: []*string{
			aws.String(*id),
		},
	}
	resp, err := clientEC2.DescribeInstances(params)
	if err != nil {
		log.Info(err)
		return ""
	}

	// https://github.com/awslabs/aws-sdk-go/blob/master/service/ec2/api.go#L13194
	return *resp.Reservations[0].Instances[0].PublicIpAddress
}
