package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"time"
)

func main() {
	config, err := ConfigFromFile("config/worker.json")
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	api := NewBlueBox(config.BlueBox)

	startTime := time.Now()
	server, err := api.Start("debug-henrikhodne-go-go-go")
	if err != nil {
		fmt.Printf("Create Error: %v\n", err)
		os.Exit(1)
	}
	defer server.Destroy()

	fmt.Println("Booting server…")

	// Wait until ready
	doneChan, cancelChan := waitFor(func() bool {
		server.Refresh()
		return server.Ready()
	}, 3*time.Second)

	select {
	case <-doneChan:
		fmt.Printf("Booted server in %.2f seconds.\n", time.Now().Sub(startTime).Seconds())
	case <-time.After(4 * time.Minute):
		fmt.Println("ERROR: Could not boot within 4 minutes")
		cancelChan <- true
		return
	}

	fmt.Println("Connecting to SSH…")
	ssh, err := NewSSHConnection(server)
	defer ssh.Close()

	fmt.Println("Connected, uploading build script")

	buildScript, err := ioutil.ReadFile("build.sh")
	if err != nil {
		fmt.Printf("Failed to read build.sh: %v\n", err)
		return
	}

	err = ssh.UploadFile("build.sh", buildScript)
	if err != nil {
		fmt.Printf("Failed to upload build.sh: %v\n", err)
		return
	}

	err = ssh.Run("chmod +x ~/build.sh")
	if  err != nil {
		fmt.Printf("Couldn't set build script as executable: %v\n", err)
	}

	outputChan, err := ssh.Start("~/build.sh")
	if err != nil {
		fmt.Printf("Failed to run build script: %v\n", err)
	}

	for {
		select {
		case bytes, open := <-outputChan:
			if !open {
				fmt.Println("Done, shutting down.")
			}
			if bytes != nil {
				fmt.Printf(">> %s\n", bytes)
			}
		case <-time.After(10 * time.Minute):
			fmt.Printf("!! No log output after 10 minutes, stopping build")
			break
		}
	}
}
