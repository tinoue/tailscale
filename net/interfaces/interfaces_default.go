// Copyright (c) 2021 Tailscale Inc & AUTHORS All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// +build !linux

package interfaces

import "inet.af/netaddr"

// On some platforms, IPv4 link-local addresses 169.254.x.y are potentially used
// with NAT for connectivity. By default though, we decline to consider them.
func isIp4LinkLocalUsable(ip netaddr.IP) bool {
	return false
}
