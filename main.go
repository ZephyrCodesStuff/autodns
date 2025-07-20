package main

import (
	"context"
	"net"
	"os"
	"regexp"
	"strings"

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
	IPAddress     net.IP
}

const TraefikLabelRegex = "traefik.http.routers.([\\w\\-\\_]+).rule=Host\\(`((?:(?:[a-zA-Z]|[a-zA-Z][a-zA-Z0-9\\-]*[a-zA-Z0-9])\\.)*(?:[A-Za-z]|[A-Za-z][A-Za-z0-9\\-]*[A-Za-z0-9]))`\\)"

func getContainers() ([]container.Summary, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, err
	}

	containers, err := cli.ContainerList(context.Background(), container.ListOptions{All: true})
	if err != nil {
		return nil, err
	}

	return containers, nil
}

func discoverTraefik() *Service {
	log.Info().Msg("Searching for Traefik services...")

	containers, err := getContainers()
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to get Docker containers")
		return nil
	}

	for _, container := range containers {
		// Search for containers where the image is `traefik`
		imageName := strings.Split(container.Image, ":")[0] // Get the image name without tag

		if imageName != "traefik" {
			continue
		}

		// Check if the container wants its own IP address
		ipAddressLabel, ok := container.Labels["com.autodns.ip"]
		if ok && ipAddressLabel != "" {
			log.Info().Msgf("Container `%s` has its own IP address specified: `%s`", container.Names[0], ipAddressLabel)
			return &Service{
				ContainerName: container.Names[0],
				HostnameLabel: "traefik",
				IPAddress:     net.ParseIP(ipAddressLabel),
			}
		}

		// Return the IP address
		network, ok := container.Labels["com.autodns.network"]
		if !ok {
			network = "bridge" // Default to bridge network if not specified
		}

		// Ensure it has either the given network or the default bridge network
		if _, exists := container.NetworkSettings.Networks[network]; !exists {
			log.Warn().Msgf("Container `%s` is not on network `%s`, skipping", container.Names[0], network)
			continue
		}

		// Return the IP in that network
		ip := container.NetworkSettings.Networks[network].IPAddress
		if ip == "" {
			log.Warn().Msgf("Container `%s` does not have an IP address in network `%s`, skipping", container.Names[0], network)
			continue
		}

		log.Info().Msgf("Found Traefik service in container `%s` with IP `%s` on network `%s`", container.Names[0], ip, network)
		return &Service{
			ContainerName: container.Names[0],
			HostnameLabel: "traefik",
			IPAddress:     net.ParseIP(ip),
		}
	}

	return nil
}

func discover() []Service {
	log.Info().Msg("Discovering services...")
	var discovered []Service

	containers, err := getContainers()
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to get Docker containers")
		return nil
	}

	// Attempt to discover Traefik first
	traefikIP := discoverTraefik()

	traefikRe := regexp.MustCompile(TraefikLabelRegex)

	for _, container := range containers {

		// Try autodns label first
		hostname, ok := container.Labels["com.autodns.hostname"]

		// If autodns label is not set, check Traefik labels
		routed := false
		if !ok || hostname == "" {
			for label, value := range container.Labels {
				matches := traefikRe.FindStringSubmatch(label + "=" + value)
				if len(matches) == 3 {
					hostname = matches[2] // 0 is the full match, 1 is the router name, 2 is the hostname
					log.Debug().Msgf("Extracted Traefik hostname `%s` for service `%s` from container `%s`", hostname, matches[1], container.Names[0])

					if traefikIP == nil {
						log.Warn().Msgf("Container `%s` has Traefik hostname `%s`, but no Traefik service discovered, skipping", container.Names[0], hostname)
						continue
					}

					// Route this service to Traefik
					discovered = append(discovered, Service{
						ContainerName: container.Names[0],
						HostnameLabel: hostname,
						IPAddress:     traefikIP.IPAddress,
					})

					log.Debug().Msgf("Container `%s` has Traefik hostname `%s`, routing to Traefik IP `%s`", container.Names[0], hostname, traefikIP.IPAddress)
					routed = true
					continue
				}
			}
		}

		// Skip to the next container if routed to Traefik
		if routed {
			continue
		}

		// If still no hostname, skip this container
		if hostname == "" {
			continue
		}

		// Check if the container wants its own IP address
		ipAddressLabel, ok := container.Labels["com.autodns.ip"]
		if ok && ipAddressLabel != "" {
			log.Info().Msgf("Container `%s` has its own IP address specified: `%s`", container.Names[0], ipAddressLabel)
			discovered = append(discovered, Service{
				ContainerName: container.Names[0],
				HostnameLabel: hostname,
				IPAddress:     net.ParseIP(ipAddressLabel),
			})
			continue
		}

		// Network selection
		network, ok := container.Labels["com.autodns.network"]
		if !ok {
			network = "bridge"
		}
		if _, exists := container.NetworkSettings.Networks[network]; !exists {
			log.Warn().Msgf("Container `%s` is not on network `%s`, skipping", container.Names[0], network)
			continue
		}

		discovered = append(discovered, Service{
			ContainerName: container.Names[0],
			HostnameLabel: hostname,
			IPAddress:     net.ParseIP(container.NetworkSettings.Networks[network].IPAddress),
		})
	}

	log.Info().Msgf("Discovered %d services:", len(discovered))
	for _, service := range discovered {
		log.Info().Msgf(" - %s (%s) -> %s", service.ContainerName, service.HostnameLabel, service.IPAddress)
	}
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
		serviceMap[service.HostnameLabel+"."] = service.IPAddress.String()
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
