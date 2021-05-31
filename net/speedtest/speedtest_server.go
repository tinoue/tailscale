// Copyright (c) 2021 Tailscale Inc & AUTHORS All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package speedtest

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"time"
)

// GetListener takes in a host and port as strings and creates and returns
// a listener for that host port pair.
func GetListener(host, port string) (*net.TCPListener, error) {
	addr, err := net.ResolveTCPAddr("tcp", host+":"+port)
	if err != nil {
		return nil, err
	}
	return net.ListenTCP("tcp", addr)
}

// StartServer starts up the server on a given host and port pair. It starts to listen for
// connections and handles each one in a goroutine. Because it runs in an infinite loop,
// this function only returns if any of the tests return with errors, or if a bool is sent
// to the killSignal channel.
func StartServer(l *net.TCPListener, maxConnections int, killSignal chan bool) error {
	defer l.Close()

	numConnections := 0
	testStateChan := make(chan TestState, maxConnections)
	connChan := make(chan *net.TCPConn, maxConnections)

	go (func() {
		for {
			conn, err := l.AcceptTCP()
			if err != nil {
				// The AcceptTCP will return an error if the listener is closed.
				return
			}
			if numConnections >= maxConnections {
				continue
			}
			connChan <- conn
		}
	})()

	for {
		select {
		case <-killSignal:
			return nil
		case conn := <-connChan:
			//handle the connection in a goroutine
			go handleConnection(conn, testStateChan)
			numConnections++
		case state := <-testStateChan:
			if state.failed {
				return state.err
			}
			numConnections--
		}
	}
}

// handleConnection reads the initial message into a TestConfig struct and
// determines what test to run. It ignores the config if the type is not
// download or upload. It sends all errors it comes across as TestStates into
// the testStateChan channel.
func handleConnection(conn *net.TCPConn, testStateChan chan TestState) {
	defer conn.Close()
	var config TestConfig
	ConfigBuffer := make([]byte, LenBufJSON)
	err := readJSON(conn, ConfigBuffer, &config)
	if err != nil {
		//fmt.Println("encountered error:", err)
		testStateChan <- TestState{failed: true, err: err}
		return
	}
	switch config.Type {
	case "download":
		// Start the download test
		err = downloadServer(conn, config)
	case "upload":
	}

	if err != nil {
		fmt.Println("error encountered:", err)
		testStateChan <- TestState{failed: true, err: err}
		return
	}
	testStateChan <- TestState{failed: false, err: nil}
}

// downloadServer runs the server side of the download test. It sends the start header, then
// for a given number of seconds, the function sends the data header with a given number of random bytes after it.
// when the test is finished, the server will send the end header. Parameters like the size of each message or the time
// the test takes must be passed in the config parameter.
func downloadServer(conn *net.TCPConn, config TestConfig) error {
	startHeader := Header{Type: Start}
	// capacity that can include headers and data
	BufData := make([]byte, config.MessageSize, LenBufJSON+config.MessageSize)
	startBytes, err := marshalJSON(startHeader)
	if err != nil {
		return err
	}
	_, err = conn.Write(startBytes)
	if err != nil {
		return err
	}
	testDuration := time.Second * time.Duration(config.Time)
	for startTime := time.Now(); time.Since(startTime) < testDuration; {
		// Reset the slices length
		BufData = BufData[:config.MessageSize]
		// Randomize data and get length
		lenDataGen, err := rand.Read(BufData)
		if err != nil {
			fmt.Println("fail to generate random data")
			continue
		}
		// Construct and marshal header
		dataHeader := Header{Type: Data, IncomingSize: lenDataGen}
		dataBytes, err := marshalJSON(dataHeader)
		if err != nil {
			continue
		}
		// Add header in front of data.
		BufData = append(dataBytes, BufData...)
		_, err = conn.Write(BufData)
		if err != nil {
			// If the write failed, there is most likely something wrong with the connection.
			return errors.New("connection closed unexpectedly")
		}

	}
	endHeader := Header{Type: End}
	headerBytes, err := marshalJSON(endHeader)
	if err != nil {
		return err
	}
	_, err = conn.Write(headerBytes)
	if err != nil {
		return err
	}
	return nil
}

// marshalJSON marshals and pads structs to json byte slices.
// It pads the byteslice so that its exactly LenBufJSON bytes.
func marshalJSON(src interface{}) ([]byte, error) {
	b, err := json.Marshal(src)
	if err != nil {
		return nil, err
	}
	if len(b) > LenBufJSON {
		return nil, errors.New("the given src is too large")
	}
	padding := make([]byte, LenBufJSON-len(b))
	b = append(b, padding...)

	return b, nil
}
