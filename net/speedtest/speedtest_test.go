// Copyright (c) 2021 Tailscale Inc & AUTHORS All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package speedtest

import (
	"fmt"
	"net"
	"testing"
)

func TestDownload(t *testing.T) {
	// start up the speedtest server with a hardcoded port and address

	//go StartServer("127.0.0.1", "8080")

	// Create a channel to signal the server to close and defer the signal
	// so that the server closes when the test ends.
	killServer := make(chan bool, 1)
	defer (func() { killServer <- true })()
	serverPort, err := getProbablyFreePortNumber()
	if err != nil {
		t.Fatal("cannot get free port number", err)
	}
	t.Log("port found:", serverPort)
	serverIP := "127.0.0.1"

	listener, err := GetListener(serverIP, serverPort)
	if err != nil {
		t.Fatal("cannot Listen on given port", serverPort)
	}

	type state struct {
		err error
	}

	stateChan := make(chan state, 2)

	go (func() {
		err := StartServer(listener, 1, killServer)
		stateChan <- state{err: err}
	})()

	conf := TestConfig{
		Type:        "download",
		Increment:   1,
		MessageSize: 32000,
		Time:        5,
	}

	go (func() {
		results, err := StartClient(conf, serverIP, serverPort)
		if err != nil {
			fmt.Println("client died")
			stateChan <- state{err: err}
		}
		for _, result := range results {
			t.Log(result.Display())
		}
		stateChan <- state{err: nil}
	})()

	testState := <-stateChan
	if testState.err != nil {
		t.Fatal(testState.err)
	}

}

func getProbablyFreePortNumber() (string, error) {
	l, err := net.Listen("tcp", ":0")
	if err != nil {
		return "", err
	}

	defer l.Close()

	_, port, err := net.SplitHostPort(l.Addr().String())
	if err != nil {
		return "", err
	}

	return port, nil
}
