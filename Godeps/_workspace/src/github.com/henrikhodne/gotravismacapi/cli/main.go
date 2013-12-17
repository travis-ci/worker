package main

import (
	"fmt"
	"github.com/henrikhodne/gotravismacapi"
	"net/url"
	"os"
)

func main() {
	urlStr := os.Getenv("TRAVIS_SAUCE_API_URI")
	if urlStr == "" {
		fmt.Println("You need to set the TRAVIS_SAUCE_API_URI environment variable")
		fmt.Println("  export TRAVIS_SAUCE_API_URI=\"http://user:password@api-endpoint:port\"")
		os.Exit(1)
	}

	u, _ := url.Parse(urlStr)
	client := gotravismacapi.NewClient(u)

	switch os.Args[1] {
	case "list":
		instances, err := client.ListInstances()
		if err != nil {
			fmt.Printf("couldn't get list of running instances: %v\n", err)
			os.Exit(1)
		}

		for _, instanceID := range instances {
			instance, err := client.InstanceInfo(instanceID)
			if err != nil {
				fmt.Printf("couldn't get info about instance %v: %v", instanceID, err)
				os.Exit(1)
			}
			fmt.Printf(`Instance ID: %v
  Image ID: %v
  Real Image ID: %v
  State: %v
  Private IP: %v
  Extra Info: %+v

`, instance.ID, instance.ImageID, instance.RealImageID, instance.State, instance.PrivateIP, instance.ExtraInfo)
		}
	}
}
