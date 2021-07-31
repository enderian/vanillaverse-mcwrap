package main

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/viper"
)

type ShutdownStep int

var currentStep ShutdownStep = 0

const (
	None               ShutdownStep = iota
	GracefullyStopping              // Send a /stop command to the server.
	Stopping                        // Send a SIGTERM to request a faster shutdown.
	Killing                         // Send a SIGKILL and kill it.
)

func shutdown(cmd *exec.Cmd, pipe io.WriteCloser) {
	if currentStep >= Killing {
		return
	}

	// Try next method of shutting down.
	currentStep += 1

	var err error
	switch currentStep {
	case GracefullyStopping:
		log.Println("trying to shutdown gracefully")
		scmd := fmt.Sprintf("\n%s\n", viper.GetString("shutdown_cmd"))
		_, err = pipe.Write([]byte(scmd))
		go reattemptShutdown(cmd, pipe, viper.GetInt64("shutdown_wait"))
	case Stopping:
		log.Println("trying to shutdown with SIGTERM")
		err = cmd.Process.Signal(syscall.SIGTERM)
		go reattemptShutdown(cmd, pipe, viper.GetInt64("term_wait"))
	case Killing:
		log.Println("trying to kill")
		err = cmd.Process.Signal(syscall.SIGKILL)
	}
	if err != nil {
		log.Printf("error while requesting shutdown: %v", err)
	}
}

func shutdownWithNotification(cmd *exec.Cmd, pipe io.WriteCloser) {
	not := fmt.Sprintf(viper.GetString("notify_cmd"), viper.GetInt("notify_wait"))
	_, err := pipe.Write([]byte(fmt.Sprintf("\n%s\n", not)))
	if err != nil {
		log.Printf("error while requesting shutdown: %v", err)
	}
	time.Sleep(time.Duration(viper.GetInt64("notify_wait")) * time.Second)
	shutdown(cmd, pipe)
}

func reattemptShutdown(cmd *exec.Cmd, pipe io.WriteCloser, wait int64) {
	time.Sleep(time.Duration(wait) * time.Second)
	// Try to shutdown again.
	shutdown(cmd, pipe)
}

func main() {
	viper.SetEnvPrefix("MCWRAP_")
	viper.SetDefault("shutdown_cmd", "stop")
	viper.SetDefault("shutdown_wait", 30)
	viper.SetDefault("term_wait", viper.GetInt64("shutdown_wait"))
	viper.SetDefault("notify_cmd", "notify_shutdown %d")
	viper.SetDefault("notify_wait", 30)
	viper.AutomaticEnv()

	cmd := exec.Command(os.Args[1], os.Args[2:]...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// Stdin redirection
	pipe, err := cmd.StdinPipe()
	if err != nil {
		panic(err)
	}
	go func() {
		rd := bufio.NewReader(os.Stdin)
		for {
			in, err := rd.ReadBytes('\n')
			if err == io.EOF {
				break
			}
			if err != nil {
				fmt.Printf("Error while reading from input: %v", err)
				break
			}
			if _, err = pipe.Write(in); err != nil {
				fmt.Printf("Error while writing to wrapped process: %v", err)
			}
		}
	}()

	// Signal handling
	sigs := make(chan os.Signal)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM, syscall.SIGUSR1)
	go func() {
		for {
			switch <-sigs {
			case syscall.SIGINT, syscall.SIGTERM:
				go shutdown(cmd, pipe)
			case syscall.SIGUSR1:
				go shutdownWithNotification(cmd, pipe)
			}
		}
	}()

	if err := cmd.Start(); err != nil {
		panic(err)
	}
	if err := cmd.Wait(); err != nil {
		panic(err)
	}
}
