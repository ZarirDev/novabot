## Client Installation

### 1. Build the Client

Navigate to your project directory and build the client using `make`:

```bash
cd ~/Documents/Programming/novabot
make client

```

### 2. Configure and Install

Run the installer script pointing to your server's IP address:

```bash
./bin/novabot-client install --server 192.168.68.XX

```

> **Note:** This command automatically generates the configuration file at `~/.config/novabot/client.json` and sets up a user systemd unit.

### 3. Set Up Sudoers for GRUB Control

To allow GRUB reboot commands to run without prompting for a password, create and edit a sudoers file:

```bash
sudo visudo -f /etc/sudoers.d/novabot-client

```

Add the following line to the file:

```text
zarir ALL=(root) NOPASSWD: /usr/sbin/grub-reboot

```

### 4. Enable and Start the Service

Finally, enable and start the user systemd service:

```bash
systemctl --user enable --now novabot-client

```