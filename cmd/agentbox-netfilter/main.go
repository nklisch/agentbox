// agentbox-netfilter is a small daemon that tails the CoreDNS sidecar's query
// log (via `podman logs --follow`) and populates a per-network ipset with each
// resolved IP, so host iptables rules can enforce IP-level egress filtering.
//
// Design decisions:
//   - Tails `podman logs --follow <container>` rather than a file because
//     CoreDNS 1.14.3's log plugin writes to stdout only (no file support).
//   - Uses stdlib flag (no cobra) — single file, minimal deps.
//   - Runs as a detached process under sudo so it has CAP_NET_ADMIN for ipset.
//   - Exits cleanly on SIGTERM or SIGINT (Manager.stopNetfilterDaemon sends SIGTERM).
//
// Usage:
//
//	agentbox-netfilter --coredns-container <name> --ipset <name>
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"regexp"
	"strings"
	"syscall"
)

// ipRE matches IPv4 addresses in CoreDNS log lines.
//
// CoreDNS 1.14.3 log format (with the `log` plugin) looks like:
//
//	[INFO] 10.89.X.3:PORT - 1234 "A IN api.github.com. udp 38 false 512" NOERROR qr,aa,rd,ra 48 0.000123456s
//
// The answer IP is embedded in the container's response, visible in subsequent
// lines. However, since we only see the query+response metadata on stdout (not
// the DNS wire response), we extract ANY IPv4 address from NOERROR lines. The
// source IP (box's address within the network) is also present but adding it
// to the ipset is benign (it's within the subnet the DROP rule allows anyway).
//
// This regex intentionally matches broadly — false positives just add harmless
// entries to the ipset (internal IPs that are already accessible).
var ipRE = regexp.MustCompile(`\b(\d{1,3}\.){3}\d{1,3}\b`)

func main() {
	corednsContainer := flag.String("coredns-container", "", "name of the CoreDNS sidecar container to tail")
	ipsetName := flag.String("ipset", "", "name of the ipset to populate with resolved IPs")
	flag.Parse()

	if *corednsContainer == "" || *ipsetName == "" {
		fmt.Fprintln(os.Stderr, "usage: agentbox-netfilter --coredns-container <name> --ipset <name>")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Tails the CoreDNS sidecar logs and populates an ipset with resolved IPs.")
		fmt.Fprintln(os.Stderr, "Must be run as root (or via sudo) because ipset requires CAP_NET_ADMIN.")
		os.Exit(2)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, os.Interrupt)
	go func() {
		<-sigCh
		cancel()
	}()

	if err := tailAndApply(ctx, *corednsContainer, *ipsetName); err != nil {
		if ctx.Err() != nil {
			// Clean shutdown via signal — not an error.
			os.Exit(0)
		}
		fmt.Fprintf(os.Stderr, "agentbox-netfilter: %v\n", err)
		os.Exit(1)
	}
}

// tailAndApply starts `podman logs --follow <container>`, reads each log line,
// and calls `ipset add <ipsetName> <ip> -exist` for each IPv4 address found
// in a NOERROR log line.
func tailAndApply(ctx context.Context, container, ipset string) error {
	cmd := exec.CommandContext(ctx, "podman", "logs", "--follow", container)

	// Use a single pipe that merges stdout+stderr from `podman logs`.
	// podman logs writes container stdout/stderr both to its own stdout,
	// so we only need to capture cmd.StdoutPipe.
	pr, pw := io.Pipe()
	cmd.Stdout = pw
	cmd.Stderr = pw

	if err := cmd.Start(); err != nil {
		_ = pr.Close()
		_ = pw.Close()
		return fmt.Errorf("podman logs --follow %s: %w", container, err)
	}

	done := make(chan error, 1)
	go func() {
		err := cmd.Wait()
		_ = pw.Close() // signal EOF to the reader
		done <- err
	}()

	if err := processLines(ctx, pr, ipset); err != nil && ctx.Err() == nil {
		return err
	}
	_ = pr.Close()

	// Wait for the command to exit (context cancel will kill it via CommandContext).
	select {
	case err := <-done:
		if ctx.Err() != nil {
			return nil // clean shutdown
		}
		return err
	case <-ctx.Done():
		return nil
	}
}

// processLines reads log lines from r and adds resolved IPs to the ipset.
func processLines(ctx context.Context, r io.Reader, ipset string) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		if ctx.Err() != nil {
			return nil
		}
		line := sc.Text()
		// Only process NOERROR lines — these represent successful resolutions
		// where the client should have gotten real IPs back.
		if !strings.Contains(line, "NOERROR") {
			continue
		}
		for _, ip := range ipRE.FindAllString(line, -1) {
			// Skip private/loopback addresses that don't need to be in the
			// allow-set (the FORWARD rule's -s already targets the subnet).
			if isPrivate(ip) {
				continue
			}
			// ipset add <name> <ip> -exist — no error on duplicate entry.
			_ = exec.Command("ipset", "add", ipset, ip, "-exist").Run()
		}
	}
	if err := sc.Err(); err != nil && ctx.Err() == nil {
		return fmt.Errorf("scan: %w", err)
	}
	return nil
}

// isPrivate reports whether ip is a private, loopback, or link-local address
// that shouldn't be added to the egress ipset.
func isPrivate(ipStr string) bool {
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return true // unparseable — skip it
	}
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast()
}
