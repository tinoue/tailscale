// Copyright (c) 2021 Tailscale Inc & AUTHORS All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
package portmapper

import (
	"context"
	"sync"
	"time"

	"inet.af/netaddr"
	"tailscale.com/net/netns"
)

type ProbeResult struct {
	PCP  bool
	PMP  bool
	UPnP bool
}

// Probe returns a summary of which port mapping services are
// available on the network.
//
// If a probe has run recently and there haven't been any network changes since,
// the returned result might be served from the Client's cache, without
// sending any network traffic.
func (c *Client) Probe(ctx context.Context) (res ProbeResult, err error) {
	gw, myIP, ok := c.gatewayAndSelfIP()
	if !ok {
		return res, ErrGatewayNotFound
	}
	defer func() {
		if err == nil {
			c.mu.Lock()
			c.lastProbe = time.Now()
			c.mu.Unlock()
		}
	}()

	uc, err := netns.Listener().ListenPacket(ctx, "udp4", ":0")
	if err != nil {
		c.logf("ProbePCP: %v", err)
		return res, err
	}
	defer uc.Close()
	ctx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
	defer cancel()
	defer closeCloserOnContextDone(ctx, uc)()

	pcpAddr := netaddr.IPPortFrom(gw, pcpPort).UDPAddr()
	pmpAddr := netaddr.IPPortFrom(gw, pmpPort).UDPAddr()

	// Don't send probes to services that we recently learned (for
	// the same gw/myIP) are available. See
	// https://github.com/tailscale/tailscale/issues/1001
	wg := sync.WaitGroup{}
	defer wg.Wait()
	if c.sawUPnPRecently() {
		res.UPnP = true
	} else {
		wg.Add(1)
		go func() {
			// TODO(jknodt) this is expensive, maybe it's worth caching it and just reusing it
			// more aggressively
			hasUPnP, _ := probeUPnP(ctx)
			if hasUPnP {
				res.UPnP = true
				c.mu.Lock()
				c.uPnPSawTime = time.Now()
				c.mu.Unlock()
			}
			wg.Done()
		}()
	}
	if c.sawPMPRecently() {
		res.PMP = true
	} else {
		uc.WriteTo(pmpReqExternalAddrPacket, pmpAddr)
	}
	if c.sawPCPRecently() {
		res.PCP = true
	} else {
		uc.WriteTo(pcpAnnounceRequest(myIP), pcpAddr)
	}

	buf := make([]byte, 1500)
	pcpHeard := false // true when we get any PCP response
	for {
		if pcpHeard && res.PMP {
			// Nothing more to discover.
			return res, nil
		}
		n, _, err := uc.ReadFrom(buf)
		if err != nil {
			if ctx.Err() == context.DeadlineExceeded {
				err = nil
			}
			return res, err
		}
		if pres, ok := parsePCPResponse(buf[:n]); ok {
			if pres.OpCode == pcpOpReply|pcpOpAnnounce {
				pcpHeard = true
				c.mu.Lock()
				c.pcpSawTime = time.Now()
				c.mu.Unlock()
				switch pres.ResultCode {
				case pcpCodeOK:
					c.logf("Got PCP response: epoch: %v", pres.Epoch)
					res.PCP = true
					continue
				case pcpCodeNotAuthorized:
					// A PCP service is running, but refuses to
					// provide port mapping services.
					res.PCP = false
					continue
				default:
					// Fall through to unexpected log line.
				}
			}
			c.logf("unexpected PCP probe response: %+v", pres)
		}
		if pres, ok := parsePMPResponse(buf[:n]); ok {
			if pres.OpCode == pmpOpReply|pmpOpMapPublicAddr && pres.ResultCode == pmpCodeOK {
				c.logf("Got PMP response; IP: %v, epoch: %v", pres.PublicAddr, pres.SecondsSinceEpoch)
				res.PMP = true
				c.mu.Lock()
				c.pmpPubIP = pres.PublicAddr
				c.pmpPubIPTime = time.Now()
				c.pmpLastEpoch = pres.SecondsSinceEpoch
				c.mu.Unlock()
				continue
			}
			c.logf("unexpected PMP probe response: %+v", pres)
		}
	}
}

type Prober struct {
	// signal the probe to stop running
	stop chan<- bool

	PMP *ProbeSubResult
	PCP *ProbeSubResult

	upnpClient upnpClient
	UPnP       *ProbeSubResult
}

// NewProber creates a new prober for a given client.
func (c *Client) NewProber(ctx context.Context) (p *Prober) {
	stop := make(chan bool)
	p = &Prober{
		stop: stop,

		PMP:  NewProbeSubResult(),
		PCP:  NewProbeSubResult(),
		UPnP: NewProbeSubResult(),
	}

	go func() {
		for {
			pmp_ctx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
			hasPCP, hasPMP, err := c.probePMPAndPCP(pmp_ctx)
			if err != nil {
				if ctx.Err() == context.DeadlineExceeded {
					err = nil
					// the global context has passed, exit cleanly
					cancel()
					return
				}
				if pmp_ctx.Err() == context.DeadlineExceeded {
					err = nil
				}
			}
			cancel()
			p.PMP.Set(hasPMP, err)
			p.PCP.Set(hasPCP, err)

			t := time.NewTimer(trustServiceStillAvailableDuration * 3 / 4)

			select {
			case <-stop:
				t.Stop()
				return
			case <-t.C: // break through and retry the connection
			}
		}
	}()

	go func() {
		// Do not timeout on getting an initial client, as we can reuse it so paying an initial cost
		// is fine.
		upnpClient, err := getUPnPClient(ctx)
		if upnpClient == nil || err != nil {
			p.UPnP.Set(false, err)
			return
		}
		p.upnpClient = upnpClient
		defer func() {
			// unset client when no longer using it.
			p.upnpClient = nil
			upnpClient.RequestTermination()
		}()
		// TODO maybe do something fancy/dynamic with more delay (exponential back-off)
		for {
			upnp_ctx, cancel := context.WithTimeout(ctx, 6*time.Second)
			retries := 0
			hasUPnP := false
			const num_connect_retries = 5
			for retries < num_connect_retries {
				status, _, _, statusErr := p.upnpClient.GetStatusInfo()
				if statusErr != nil {
					err = statusErr
					break
				}
				hasUPnP = hasUPnP || status == "Connected"
				if status == "Disconnected" {
					upnpClient.RequestConnection()
				}
				retries += 1
			}
			// need to manually check these since GetStatusInfo doesn't take a context
			if ctx.Err() == context.DeadlineExceeded {
				err = nil
				// the global context has passed, exit cleanly
				cancel()
				return
			}
			if upnp_ctx.Err() == context.DeadlineExceeded {
				err = nil
			}
			cancel()
			p.UPnP.Set(hasUPnP, err)

			t := time.NewTimer(trustServiceStillAvailableDuration * 3 / 4)

			select {
			case <-stop:
				t.Stop()
				return
			case <-t.C: // break through and retry the connection
			}
		}
	}()

	return
}

// Stop gracefully turns the Prober off.
func (p *Prober) Stop() {
	close(p.stop)
}

// CurrentStatus returns the current results of the prober, regardless of whether they have
// completed or not.
func (p *Prober) CurrentStatus() (res ProbeResult, err error) {
	hasPMP, errPMP := p.PMP.PresentCurrent()
	res.PMP = hasPMP
	err = errPMP

	hasUPnP, errUPnP := p.UPnP.PresentCurrent()
	res.UPnP = hasUPnP
	if err == nil {
		err = errUPnP
	}

	hasPCP, errPCP := p.PCP.PresentCurrent()
	res.PCP = hasPCP
	if err == nil {
		err = errPCP
	}
	return
}

type ProbeSubResult struct {
	cond *sync.Cond
	// If this probe has finished, regardless of success or failure
	completed bool

	// whether or not this feature is present
	present bool
	// most recent error
	err error

	// time we last saw it to be available.
	sawTime time.Time
}

func NewProbeSubResult() *ProbeSubResult {
	return &ProbeSubResult{
		cond: &sync.Cond{
			L: &sync.Mutex{},
		},
	}
}

// PresentBlock blocks until the probe completes, then returns the result.
func (psr *ProbeSubResult) PresentBlock() (bool, error) {
	psr.cond.L.Lock()
	defer psr.cond.L.Unlock()
	for !psr.completed {
		psr.cond.Wait()
	}
	return psr.present, psr.err
}

// PresentCurrent returns the current state, regardless whether or not the probe has completed.
func (psr *ProbeSubResult) PresentCurrent() (bool, error) {
	psr.cond.L.Lock()
	defer psr.cond.L.Unlock()
	present := psr.present && psr.sawTime.After(time.Now().Add(-trustServiceStillAvailableDuration))
	return present, psr.err
}

func (psr *ProbeSubResult) Set(present bool, err error) {
	saw := time.Now()
	psr.cond.L.Lock()
	psr.sawTime = saw
	psr.completed = true
	psr.err = err
	psr.present = present
	psr.cond.L.Unlock()

	psr.cond.Broadcast()
}

func (c *Client) probePMPAndPCP(ctx context.Context) (pcp bool, pmp bool, err error) {
	gw, myIP, ok := c.gatewayAndSelfIP()
	if !ok {
		return false, false, ErrGatewayNotFound
	}

	uc, err := netns.Listener().ListenPacket(ctx, "udp4", ":0")
	if err != nil {
		c.logf("ProbePCP/PMP: %v", err)
		return false, false, err
	}
	defer uc.Close()
	defer closeCloserOnContextDone(ctx, uc)()

	pcpAddr := netaddr.IPPortFrom(gw, pcpPort).UDPAddr()
	pmpAddr := netaddr.IPPortFrom(gw, pmpPort).UDPAddr()

	// Don't send probes to services that we recently learned (for
	// the same gw/myIP) are available. See
	// https://github.com/tailscale/tailscale/issues/1001
	if c.sawPMPRecently() {
		pmp = true
	} else {
		uc.WriteTo(pmpReqExternalAddrPacket, pmpAddr)
	}
	if c.sawPCPRecently() {
		pcp = true
	} else {
		uc.WriteTo(pcpAnnounceRequest(myIP), pcpAddr)
	}

	buf := make([]byte, 1500)
	pcpHeard := false // true when we get any PCP response
	for {
		if pcpHeard && pmp {
			// Nothing more to discover.
			return
		}
		n, _, err := uc.ReadFrom(buf)
		if err != nil {
			if ctx.Err() == context.DeadlineExceeded {
				err = nil
			}
			return pcp, pmp, err
		}
		if pres, ok := parsePCPResponse(buf[:n]); ok {
			if pres.OpCode == pcpOpReply|pcpOpAnnounce {
				pcpHeard = true
				c.mu.Lock()
				c.pcpSawTime = time.Now()
				c.mu.Unlock()
				switch pres.ResultCode {
				case pcpCodeOK:
					c.logf("Got PCP response: epoch: %v", pres.Epoch)
					pcp = true
					continue
				case pcpCodeNotAuthorized:
					// A PCP service is running, but refuses to
					// provide port mapping services.
					pcp = false
					continue
				default:
					// Fall through to unexpected log line.
				}
			}
			c.logf("unexpected PCP probe response: %+v", pres)
		}
		if pres, ok := parsePMPResponse(buf[:n]); ok {
			if pres.OpCode == pmpOpReply|pmpOpMapPublicAddr && pres.ResultCode == pmpCodeOK {
				c.logf("Got PMP response; IP: %v, epoch: %v", pres.PublicAddr, pres.SecondsSinceEpoch)
				pmp = true
				c.mu.Lock()
				c.pmpPubIP = pres.PublicAddr
				c.pmpPubIPTime = time.Now()
				c.pmpLastEpoch = pres.SecondsSinceEpoch
				c.mu.Unlock()
				continue
			}
			c.logf("unexpected PMP probe response: %+v", pres)
		}
	}
}
