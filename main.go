package main

import (
	"context"
	"net"
	"os"

	// DNS server
	"github.com/miekg/dns"

	// Docker client
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"

	// Logging
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func makeResponse(h string, ip net.IP) *dns.Msg {
	log.Debug().Msgf("Creating DNS response for: %s", h)

	records := []dns.RR{
		&dns.A{
			Hdr: dns.RR_Header{
				Name:   h,
				Rrtype: dns.TypeA,
				Class:  dns.ClassINET,
				Ttl:    3600,
			},
			A: ip,
		},
	}

	m := new(dns.Msg)
	m.SetReply(&dns.Msg{})
	m.Authoritative = true
	m.RecursionAvailable = true
	m.Compress = false
	m.Answer = records

	return m
}

type Service struct {
	ContainerName string
	HostnameLabel string
	IPAddress     string
}

func discover() []Service {
	log.Info().Msg("Discovering services...")
	var discovered []Service

	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to create Docker client")
		return nil
	}

	containers, err := cli.ContainerList(context.Background(), container.ListOptions{All: true})
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to list Docker containers")
		return nil
	}

	for _, container := range containers {

		hostname, ok := container.Labels["com.autodns.hostname"]
		if !ok {
			log.Debug().Msgf("Container `%s` does not have `com.autodns.hostname` label, skipping", container.Names[0])
			continue
		}

		// The container's network the user wants to get the IP from
		network, ok := container.Labels["com.autodns.network"]
		if !ok {
			network = "bridge" // Default to bridge network if not specified
		}

		// Ensure the container really is on the specified network
		if _, exists := container.NetworkSettings.Networks[network]; !exists {
			log.Warn().Msgf("Container `%s` is not on network `%s`, skipping", container.Names[0], network)
			continue
		}

		discovered = append(discovered, Service{
			ContainerName: container.Names[0],
			HostnameLabel: hostname,
			IPAddress:     container.NetworkSettings.Networks[network].IPAddress,
		})
	}

	log.Info().Msgf("Discovered %d services", len(discovered))
	return discovered
}

func main() {
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stdout, NoColor: false})
	log.Info().Msg("Starting AutoDNS...")

	serverUDP := &dns.Server{
		Addr: ":53",
		Net:  "udp",
	}
	serverTCP := &dns.Server{
		Addr: ":53",
		Net:  "tcp",
	}

	// Discover services
	services := discover()
	if len(services) == 0 {
		log.Warn().Msg("No services discovered, DNS server will not respond to queries")
	}

	go func() {
		if err := serverUDP.ListenAndServe(); err != nil {
			log.Fatal().Err(err).Msg("Failed to start UDP DNS server")
		}
	}()
	go func() {
		if err := serverTCP.ListenAndServe(); err != nil {
			log.Fatal().Err(err).Msg("Failed to start TCP DNS server")
		}
	}()

	// Build a map for quick lookup
	serviceMap := make(map[string]string)
	for _, service := range services {
		serviceMap[service.HostnameLabel+"."] = service.IPAddress
	}

	dns.HandleFunc(".", func(w dns.ResponseWriter, r *dns.Msg) {
		if len(r.Question) == 0 {
			log.Warn().Msg("Received DNS query with no questions")
			return
		}
		q := r.Question[0]
		name := q.Name
		ip, ok := serviceMap[name]
		if !ok {
			log.Warn().Msgf("No service found for hostname: %s", name)
			m := new(dns.Msg)
			m.SetReply(r)
			w.WriteMsg(m) // Empty response
			return
		}
		resp := makeResponse(name, net.ParseIP(ip))
		resp.SetReply(r)
		if err := w.WriteMsg(resp); err != nil {
			log.Error().Err(err).Msgf("Failed to write DNS response for %s", name)
			return
		}
		log.Info().Msgf("DNS response sent for %s: %s", name, ip)
	})

	log.Info().Msg("DNS server started")

	// Wait forever
	select {}
}
