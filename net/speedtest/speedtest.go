// Copyright (c) 2021 Tailscale Inc & AUTHORS All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package speedtest

import (
	"fmt"
)

const (
	Start = "start" // Start the test.
	End   = "end"   // End the test.
	Data  = "data"  // Message contains data.

	LenBufJSON    int = 100   // agreed upon before hand. Buffer size for json messages.
	MaxLenBufData int = 32000 // max buffer size for random bytes `
	DefaultTime   int = 5     // default time for a test
)

// This struct gives information during the test. For example, a header with the type being start
// starts the test.
type Header struct {
	Type         string `json:"type"`
	IncomingSize int    `json:"incoming_size,omitempty"`
}

// This is the initial message sent to the sever, that contains information on how to
// conduct the test.
type TestConfig struct {
	Type        string `json:"type"`
	MessageSize int    `json:"size,omitempty"`
	Time        int    `json:"time,omitempty"`
	Increment   int    `json:"inc,omitempty"`
}

// This represents the Result of a speedtest within a specific interval
type Result struct {
	startTime  float64
	endTime    float64
	mbRecieved float64
	secPassed  float64
}

// Returns a nicely formatted string to use when displaying the speeds in each result.
func (r Result) Display() string {
	s := "--------------------------------\n"
	if r.startTime != -1 {
		s = s + fmt.Sprintf("between  %.2f seconds and %.2f seconds:\n", r.startTime, r.endTime)
		s = s + fmt.Sprintf("recieved %.4f mb in %.2f second(s)\n", r.mbRecieved, r.secPassed)
	} else {
		s = s + "Total Speed\n"
		s = s + fmt.Sprintf("recieved %.4f mb in %.3f second(s)\n", r.mbRecieved, r.secPassed)
	}
	s = s + fmt.Sprintf("download speed: %.4f mb/s\n", r.mbRecieved/r.secPassed)
	return s
}

// TestState is used by the server when checking the result of a test.
type TestState struct {
	failed bool
	err    error
}
