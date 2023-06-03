package main

import (
	"context"
	"flag"
	"fmt"
	"time"

	agent "github.com/karimra/srl-ndk-demo"
	"github.com/karimra/srl-snmp-traps/app"
	log "github.com/sirupsen/logrus"
	"google.golang.org/grpc/metadata"
)

const (
	retryInterval = 2 * time.Second
	agentName     = "snmp-traps"
)

var version = "dev"

func main() {
	trapDir := flag.String("trap-dir", "./traps", "directory containing trap definition files")
	debug := flag.Bool("d", false, "turn on debug")
	versionFlag := flag.Bool("v", false, "print version")
	flag.Parse()

	if *versionFlag {
		fmt.Println(version)
		return
	}
	if *debug {
		log.SetLevel(log.DebugLevel)
		log.SetReportCaller(true)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx = metadata.AppendToOutgoingContext(ctx, "agent_name", agentName)

CRAGENT:
	agt, err := agent.New(ctx, agentName)
	if err != nil {
		log.Errorf("failed to create NDK agent %q: %v", agentName, err)
		log.Infof("retrying in %s", retryInterval)
		time.Sleep(retryInterval)
		goto CRAGENT
	}

	trapApp := app.New(
		app.WithAgent(agt),
		app.WithDebug(*debug),
		app.WithTrapDir(*trapDir))

	log.Infof("starting App config handler...")
	trapApp.Run(ctx)
}
