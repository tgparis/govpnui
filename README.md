# GoVPNUI

GoVPNUI is a lightweight web interface for managing StrongSwan IPsec tunnels. It consists of a Go-based backend (using the VICI API to control connections) and a static HTML/JavaScript frontend.

## Features

* List all configured child SAs (grouped by parent IKE connection)
* Detect and indicate active tunnels (solid green)
* Show live traffic statistics (bytes / packets)
* Expandable details including local/remote IPs, encryption algorithm and timers
* Initiate / terminate child SAs directly from the UI
* Search bar (supports name or IP)
* Expand/collapse per-group and "Expand all / Collapse all"

## Quick Start

```bash
# Install strongswan and golang
sudo apt install strongswan swanctl golang

# Build and run the backend
cd govpnui
go build -o govpnui
sudo ./govpnui &
# or: sudo go run main.go

# Open the web UI
http://<server>:8080/
```

## Project Structure

| Path              | Description              |
| ----------------- | ------------------------ |
| main.go           | Backend REST/VICI server |
| static/index.html | Frontend (HTML/JS)       |

## Notes

More functionality coming.

