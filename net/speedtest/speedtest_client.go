// Copyright (c) 2021 Tailscale Inc & AUTHORS All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package speedtest

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"time"
)

// StartClient dials the given address and starts a speedtest.
// It returns any errors that come up in the tests.
// It returns an error if the given test type isn't either download or upload.
// If there are no errors in the test, it returns a slice of results.
func StartClient(config TestConfig, host, port string) ([]Result, error) {
	serverAddr, err := net.ResolveTCPAddr("tcp", host+":"+port)
	if err != nil {
		return nil, err
	}
	conn, err := net.DialTCP("tcp", nil, serverAddr)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	switch config.Type {
	case "download":
		conn.SetReadBuffer(LenBufJSON + MaxLenBufData)
		configBytes, err := marshalJSON(config)
		if err != nil {
			return nil, err
		}
		conn.Write(configBytes)
		return downloadClient(conn, config)
	case "upload":
		return nil, errors.New("not implemented yet")
	default:
		return nil, errors.New("test type invalid. Must be either download or uplaod")
	}
}

// readJSON reads LenBufJSON number of bytes from the connection.
// It trims the result and attempts to unmarshal the result into the given interface.
// The given buffer must have a capacity larger than LenBufJSON.
func readJSON(conn *net.TCPConn, buffer []byte, dest interface{}) error {
	if cap(buffer) < LenBufJSON {
		return errors.New("given buffer's capacity is too small")
	}
	buffer = buffer[:LenBufJSON]
	_, err := io.ReadFull(conn, buffer)
	if err != nil {
		return err
	}

	buffer = bytes.TrimRight(buffer, "\x00")

	err = json.Unmarshal(buffer, dest)
	if err != nil {
		fmt.Println(err)
	}
	return err
}

// readData reads lenBufData number of bytes from the connection.
// It returns an error if the given buffer's capacity is smaller than lenBufData.
func readData(conn *net.TCPConn, buffer []byte, lenBufData int) error {
	if cap(buffer) < lenBufData {
		return errors.New("given buffer's capacity is too small")
	}
	buffer = buffer[:lenBufData]
	_, err := io.ReadFull(conn, buffer)
	if err != nil {
		fmt.Println("read error")
		fmt.Println(err)
		return err
	}

	return nil
}

// downloadClient handles the entire download speed test.
// It has a loop that breaks if the connection recieves an IO error or if the server sends a header
// with the "end" type. It reads the headers and data coming from the server and records the number of bytes recieved in each interval in a result slice.
func downloadClient(conn *net.TCPConn, config TestConfig) ([]Result, error) {
	bufferData := make([]byte, MaxLenBufData)
	var downloadBegin time.Time

	sum := 0
	totalSum := 0
	var lastCalculated float64 = 0.0
	breakLoop := false
	var results []Result

	for {
		var header Header
		err := readJSON(conn, bufferData, &header)
		if err != nil {
			//worst case scenario: the server closes the connection and the client quits
			if err == io.EOF {
				return nil, errors.New("connection closed unexpectedly")
			}
			return nil, errors.New("unexpected error has occured")
		}

		since := time.Since(downloadBegin)
		switch header.Type {
		case Start:
			downloadBegin = time.Now()
			since = 0
			sum += LenBufJSON
		case End:
			sum += LenBufJSON

			breakLoop = true
		case Data:
			if err = readData(conn, bufferData, header.IncomingSize); err != nil {
				return nil, errors.New("failed to read incoming data")
			}
			sum += LenBufJSON + header.IncomingSize
		}

		if breakLoop {
			var result *Result
			if int(since.Seconds()) > config.Increment {
				secPassed := since.Seconds() - lastCalculated
				result = calcStats(sum, secPassed, lastCalculated)
				if result != nil {
					results = append(results, *result)
				}
			}
			totalSum += sum
			result = calcStats(totalSum, since.Seconds(), -1)
			if result != nil {
				results = append(results, *result)
			}
			return results, nil
		}

		if since.Seconds() >= lastCalculated+float64(config.Increment) {
			secPassed := since.Seconds() - lastCalculated
			result := calcStats(sum, secPassed, lastCalculated)
			if result != nil {
				results = append(results, *result)
			}
			lastCalculated += float64(config.Increment)
			totalSum += sum
			sum = 0
		}

	}

}

// calcStats calculates the bytes received over a given interval, as well as the
// start and end for an interval. It saves this data into a Result struct, which it returns.
// If finding the Result for the total speedtest, the startTime should be -1.
func calcStats(sum int, secPassed float64, startTime float64) *Result {
	//return early if it's not worth displaying the data
	if secPassed < 0.01 {
		return nil
	}
	r := &Result{}
	r.mbRecieved = float64(sum) / 1000000.0
	r.startTime = startTime
	r.secPassed = secPassed
	if startTime != -1 {
		r.endTime = math.Round(startTime + secPassed)
		if r.endTime == startTime {
			r.endTime = startTime + secPassed
		}
	}
	return r
}
