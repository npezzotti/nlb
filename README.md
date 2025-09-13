# nlb
## Overview

**nlb** is a Layer 4 load balancer written in Go. It efficiently distributes TCP/UDP traffic across multiple backend servers, providing reliability and scalability for networked applications.

## Features

- Supports TCP and UDP protocols
- Round Robin load balancing algorithm
- Health checks for backend servers
- UI for monitoring backend status

## Getting Started

### Prerequisites

- Go 1.24.4 or higher

### Installation

```bash
git clone https://github.com/npezzotti/nlb.git
cd nlb
make run
```

### Usage

```bash
./nlb <path_to_config_file>
```

See the `examples/` directory for a sample configuration files.

