// Copyright (c) 2021 Tailscale Inc & AUTHORS All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"strings"

	"tailscale.com/client/tailscale"
	"tailscale.com/net/speedtest"

	"github.com/peterbourgon/ff/v2/ffcli"
)

// Speedtest command that contains the server and client sub commands.
var speedtestCmd = &ffcli.Command{
	Name:       "speedtest",
	ShortUsage: "speedtest <server|client> ...",
	ShortHelp:  "Run a speed test",
	Subcommands: []*ffcli.Command{
		speedtestServerCmd,
		speedtestClientCmd,
	},
	Exec: func(context.Context, []string) error {
		return errors.New("subcommand required; run 'tailscale speedtest -h' for details")
	},
}

// speedtestServerCmd takes necessary info like the port to
// listen on and then passes them to the StartServer function in the speedtest package.
// if the localhost flag is given, the server will use 127.0.0.1, otherwise the server will
// use the tailscale ip address
var speedtestServerCmd = &ffcli.Command{
	Name:       "server",
	ShortUsage: "speedtest server -host <host> -port <port> -maxConn <max connections>",
	ShortHelp:  "Start a speed test server",
	Exec:       runServer,
	FlagSet: (func() *flag.FlagSet {
		fs := flag.NewFlagSet("server", flag.ExitOnError)
		fs.IntVar(&serverArgs.port, "port", 0, "port to listen on")
		fs.BoolVar(&serverArgs.localhost, "localhost", false, "use localhost or tailscale ip")
		fs.IntVar(&serverArgs.maxConnections, "maxConn", 1, "max number of concurrent connections allowed")
		return fs
	})(),
}

// speedtestClientCmd takes info like the type of test to run, message size, test time, and the host and port
// of the speedtest server and passes them to the StartClient function in the speedtest package.
var speedtestClientCmd = &ffcli.Command{
	Name:       "client",
	ShortUsage: "speedtest client <-d|-u> -host <host> -port <port> -inc <increment> -size <message size>",
	ShortHelp:  "Start a speed test client and connect to a speed test server",
	Exec:       runClient,
	FlagSet: (func() *flag.FlagSet {
		fs := flag.NewFlagSet("client", flag.ExitOnError)
		fs.StringVar(&clientArgs.host, "host", "", "The ip address for the speedtest server being used")
		fs.StringVar(&clientArgs.port, "port", "", "The port of the speedtest server being used")
		fs.IntVar(&clientArgs.inc, "inc", 1, "The increment for displaying speedtest info")
		fs.BoolVar(&clientArgs.download, "d", false, "Include this to run a download test")
		fs.BoolVar(&clientArgs.upload, "u", false, "Include this to run an upload test")
		fs.IntVar(&clientArgs.size, "size", speedtest.MaxLenBufData, "The size of the messages sent over TCP")
		fs.IntVar(&clientArgs.time, "time", speedtest.DefaultTime, "The duration of the speed test")
		return fs
	})(),
}

var serverArgs struct {
	port           int
	localhost      bool
	maxConnections int
}

// runServer takes the port from the serverArgs variable, finds the tailscale ip if needed, then passes them
// to speedtest.GetListener. The listener is then passed to speedtest.StartServer. No channel is passed to
// StartServer, because to kill the server all the user has to do is do Ctrl+c.
func runServer(ctx context.Context, args []string) error {
	if serverArgs.port == 0 {
		return errors.New("port needs to be provided")
	}

	portString := fmt.Sprint(serverArgs.port)
	hostString := "127.0.0.1"

	if !serverArgs.localhost {
		st, err := tailscale.Status(ctx)
		if err != nil {
			return err
		}
		ips := st.TailscaleIPs
		if len(ips) == 0 {
			return errors.New("no tailscale ips found")
		}
		for _, ip := range ips {
			if ip.Is4() {
				hostString = ip.String()
			}
		}
	}
	listener, err := speedtest.GetListener(hostString, portString)
	if err != nil {
		return err
	}
	fmt.Println("listening on", hostString+":"+portString, "...")

	return speedtest.StartServer(listener, serverArgs.maxConnections, nil)
}

var clientArgs struct {
	download bool
	upload   bool
	inc      int
	time     int
	size     int
	host     string
	port     string
}

// runClient checks that the given parameters are within the allowed range. It also checks
// that both the host and port of the server are given. It passes the parameters to the
// startClient function in the speedtest package. It then prints the results that are returned.
func runClient(ctx context.Context, args []string) error {
	if strings.EqualFold(clientArgs.host, "") || strings.EqualFold(clientArgs.port, "") {
		return errors.New("both host and port must be given")
	}
	var config speedtest.TestConfig
	// configure the time
	if clientArgs.time < 5 || clientArgs.time > 30 {
		config.Time = 5
	} else {
		config.Time = clientArgs.time
	}

	// configure the increment
	if clientArgs.inc < 1 || clientArgs.inc >= config.Time {
		config.Increment = 1
	} else {
		config.Increment = clientArgs.inc
	}

	// configure the size
	if clientArgs.size < 0 || clientArgs.size > speedtest.MaxLenBufData {
		config.MessageSize = speedtest.MaxLenBufData
	} else {
		config.MessageSize = clientArgs.size
	}

	// configure the Type
	if clientArgs.download && clientArgs.upload {
		return errors.New("cannot do both upload and download yet")
	}
	if !clientArgs.download && !clientArgs.upload {
		return errors.New("need to pass either download or upload")
	}
	if clientArgs.download {
		config.Type = "download"
	}
	if clientArgs.upload {
		config.Type = "upload"
	}

	fmt.Printf("Starting a %s test with %s:%s ...\n", config.Type, clientArgs.host, clientArgs.port)
	results, err := speedtest.StartClient(config, clientArgs.host, clientArgs.port)
	if err != nil {
		return err
	}
	fmt.Println("Results:")
	for _, result := range results {
		fmt.Print(result.Display())
	}
	return nil
}
