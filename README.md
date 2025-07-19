# 📡 AutoDNS

AutoDNS is a cloud-native DNS server for Docker environments. It automatically discovers running containers and exposes their IP addresses via DNS, using Docker labels for configuration. 🐳

## ✨ Features

- Automatic discovery of Docker containers
- DNS responses based on container labels
- Supports both UDP and TCP DNS queries
- Configurable via Docker labels:
  - `com.autodns.hostname`: The DNS hostname to register
  - `com.autodns.network`: The Docker network to use for IP resolution (defaults to `bridge`)

## ▶️ Usage

### 🐳 Docker Run

```sh
docker run --rm --name autodns \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -p 53:53/tcp -p 53:53/udp \
  ghcr.io/zephyrcodesstuff/autodns:latest
```

### 🛠️ Docker Compose

See `docker-compose.yml` for an example setup.

## ⚡ How It Works

- AutoDNS queries the Docker API for running containers
- Containers with the `com.autodns.hostname` label are registered as DNS records
  - The `com.autodns.network` label specifies which Docker network to use for resolving the container's IP address. Default is `bridge`.
- DNS queries for these hostnames return the container's IP address on the specified network.

## 🏷️ Example Container Labels

```yaml
labels:
  com.autodns.hostname: myapp.local
  com.autodns.network: bridge
```

## 📋 Requirements

- Docker Engine
- Access to `/var/run/docker.sock`
- Ports 53/udp and 53/tcp available
