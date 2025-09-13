# Telegram IP Blocking Bot for Caddy

A Telegram bot that allows adding IP addresses to a Caddy configuration and reloading the server.

## Features

- Add IP addresses via Telegram command
- Automatic IP address validation
- Automatic backup of Caddy configuration
- Automatic Caddy server reload
- Access control via Chat ID

## Requirements

- Go 1.19 or higher
- Docker with Caddy container
- Telegram Bot Token
- Access to Docker socket

## Environment Variables

```bash
TELEGRAM_TOKEN=your_bot_token        # Required: Telegram Bot API token
ALLOWED_CHAT_ID=your_chat_id        # Required: Authorized Telegram chat ID
CADDYFILE_PATH=/etc/caddy/Caddyfile # Optional: Path to Caddyfile (default: /etc/caddy/Caddyfile)
CADDY_CONTAINER=caddy               # Optional: Caddy container name (default: caddy)
DOCKER_SOCK=/var/run/docker.sock    # Optional: Docker socket path (default: /var/run/docker.sock)
```

## Installation

1. Clone the repository:

```bash
git clone https://github.com/jumoog/telegram.git
cd telegram
```

2. Build the application:

```bash
go build -o telegram-bot
```

3. Run the bot:

```bash
./telegram-bot
```

## Caddy Configuration

Your Caddyfile must contain a marker where new IPs will be inserted:

```caddyfile
handle @site {
    @blocked {
        # add here
    }
}
```

## Usage

Send IP addresses to the bot using either:

1. Command format:

```
/addip 192.168.1.1
```

2. Direct IP format:

```
192.168.1.1
```

The bot will:

- Validate the IP address
- Create a backup of the current Caddyfile
- Add the IP to the configuration
- Reload the Caddy server

## Development

### Running Tests

```bash
go test -v .\...
```

### Building

```bash
go build -o telegram-bot
```

### Project Structure

```
telegram/
├── app.go           # Main application code
└── README.md        # This file
```

## Security Considerations

- Always set `ALLOWED_CHAT_ID` to prevent unauthorized access
- Secure access to the Docker socket
- Keep regular backups of your Caddyfile
- Run the bot with minimal required permissions
