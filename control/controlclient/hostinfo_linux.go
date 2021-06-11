// Copyright (c) 2020 Tailscale Inc & AUTHORS All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// +build linux,!android

package controlclient

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"strings"
	"syscall"

	"go4.org/mem"
	"tailscale.com/util/lineread"
	"tailscale.com/version/distro"
)

func init() {
	osVersion = osVersionLinux
}

func osVersionLinux() string {
	dist := distro.Get()
	propFile := "/etc/os-release"
	switch dist {
	case distro.Synology:
		propFile = "/etc.defaults/VERSION"
	case distro.OpenWrt:
		propFile = "/etc/openwrt_release"
	}

	m := map[string]string{}
	lineread.File(propFile, func(line []byte) error {
		eq := bytes.IndexByte(line, '=')
		if eq == -1 {
			return nil
		}
		k, v := string(line[:eq]), strings.Trim(string(line[eq+1:]), `"'`)
		m[k] = v
		return nil
	})

	var un syscall.Utsname
	syscall.Uname(&un)

	var attrBuf strings.Builder
	attrBuf.WriteString("; kernel=")
	for _, b := range un.Release {
		if b == 0 {
			break
		}
		attrBuf.WriteByte(byte(b))
	}
	if inContainer() {
		attrBuf.WriteString("; container")
	}
	if inKnative() {
		attrBuf.WriteString("; env=kn")
	}
	if inAwsLambda() {
		attrBuf.WriteString("; env=lm")
	}
	if inHerokuDyno() {
		attrBuf.WriteString("; env=hr")
	}
	attr := attrBuf.String()

	id := m["ID"]

	switch id {
	case "debian":
		slurp, _ := ioutil.ReadFile("/etc/debian_version")
		return fmt.Sprintf("Debian %s (%s)%s", bytes.TrimSpace(slurp), m["VERSION_CODENAME"], attr)
	case "ubuntu":
		return fmt.Sprintf("Ubuntu %s%s", m["VERSION"], attr)
	case "", "centos": // CentOS 6 has no /etc/os-release, so its id is ""
		if cr, _ := ioutil.ReadFile("/etc/centos-release"); len(cr) > 0 { // "CentOS release 6.10 (Final)
			return fmt.Sprintf("%s%s", bytes.TrimSpace(cr), attr)
		}
		fallthrough
	case "fedora", "rhel", "alpine", "nixos":
		// Their PRETTY_NAME is fine as-is for all versions I tested.
		fallthrough
	default:
		if v := m["PRETTY_NAME"]; v != "" {
			return fmt.Sprintf("%s%s", v, attr)
		}
	}
	switch dist {
	case distro.Synology:
		return fmt.Sprintf("Synology %s%s", m["productversion"], attr)
	case distro.OpenWrt:
		return fmt.Sprintf("OpenWrt %s%s", m["DISTRIB_RELEASE"], attr)
	}
	return fmt.Sprintf("Other%s", attr)
}

func inContainer() (ret bool) {
	lineread.File("/proc/1/cgroup", func(line []byte) error {
		if mem.Contains(mem.B(line), mem.S("/docker/")) ||
			mem.Contains(mem.B(line), mem.S("/lxc/")) {
			ret = true
			return io.EOF // arbitrary non-nil error to stop loop
		}
		return nil
	})
	lineread.File("/proc/mounts", func(line []byte) error {
		if mem.Contains(mem.B(line), mem.S("fuse.lxcfs")) {
			ret = true
			return io.EOF
		}
		return nil
	})
	return
}

func inKnative() bool {
	// https://cloud.google.com/run/docs/reference/container-contract#env-vars
	if os.Getenv("K_REVISION") != "" && os.Getenv("K_CONFIGURATION") != "" &&
		os.Getenv("K_SERVICE") != "" && os.Getenv("PORT") != "" {
		return true
	}
	return false
}

func inAwsLambda() bool {
	// https://docs.aws.amazon.com/lambda/latest/dg/configuration-envvars.html
	if os.Getenv("AWS_LAMBDA_FUNCTION_NAME") != "" &&
		os.Getenv("AWS_LAMBDA_FUNCTION_VERSION") != "" &&
		os.Getenv("AWS_LAMBDA_INITIALIZATION_TYPE") != "" &&
		os.Getenv("AWS_LAMBDA_RUNTIME_API") != "" {
		return true
	}
	return false
}
func inHerokuDyno() bool {
	// https://devcenter.heroku.com/articles/dynos#local-environment-variables
	if os.Getenv("PORT") != "" && os.Getenv("DYNO") != "" {
		return true
	}
	return false
}
