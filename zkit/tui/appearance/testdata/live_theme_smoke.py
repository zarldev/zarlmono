#!/usr/bin/env python3
"""Offline PTY proof that a global theme command updates inherited live apps.
Uses fresh caller-supplied binaries and disposable homes/profiles; never connects
accounts, sends messages, plays audio, or registers desktop media controls.
"""
import argparse
import fcntl
import json
import os
from pathlib import Path
import pty
import select
import struct
import subprocess
import tempfile
import termios
import time


def foreground(name):
    path = Path(__file__).resolve().parents[2] / "theme" / "themes" / (name + ".json")
    value = json.loads(path.read_text())["primary"]
    if isinstance(value, dict):
        value = value.get("dark") or value["light"]
    value = value.removeprefix("#")
    if len(value) == 3:
        value = "".join(c * 2 for c in value)
    return ("38;2;" + ";".join(str(int(value[i:i+2], 16)) for i in (0, 2, 4))).encode()


def run(reader, writer, player):
    with tempfile.TemporaryDirectory(prefix="zarl-family-live-") as temporary:
        root = Path(temporary)
        workspace = root / "workspace"
        workspace.mkdir()
        env = dict(os.environ, HOME=str(root), XDG_CONFIG_HOME=str(root / "config"),
                   XDG_DATA_HOME=str(root / "data"), TERM="xterm-ghostty",
                   TERM_PROGRAM="ghostty", COLORTERM="truecolor", CLICOLOR_FORCE="1")
        env.pop("NO_COLOR", None)
        master, slave = pty.openpty()
        child = None
        phase = bytearray()
        output = bytearray()

        def read_once(timeout=0.05):
            if select.select([master], [], [], timeout)[0]:
                try:
                    data = os.read(master, 65536)
                except OSError:
                    return
                phase.extend(data)
                output.extend(data)
                if b"\x1b[6n" in data:
                    os.write(master, b"\x1b[1;1R")

        def wait(fragment, seconds=12):
            deadline = time.monotonic() + seconds
            while time.monotonic() < deadline:
                read_once()
                if fragment in phase:
                    return
                if child.poll() is not None:
                    raise AssertionError(f"{reader} exited {child.returncode}")
            raise AssertionError(f"{reader} did not render expected theme {fragment!r}")

        try:
            fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 30, 100, 1000, 600))
            args = [reader, "--demo", "--data-dir", str(root / "profile"), "--workspace", str(workspace)]
            if player:
                args.append("--no-mpris")
            child = subprocess.Popen(args, stdin=slave, stdout=slave, stderr=slave, env=env)
            wait(b"playlists" if player else b"conversations")
            wait(foreground("ansi-bbs"))
            phase.clear()
            subprocess.run([writer, "--theme", "retro-amber", "--theme-scope", "global",
                            "--workspace", str(workspace)], env=env, stdout=subprocess.PIPE,
                           stderr=subprocess.PIPE, timeout=8, check=True)
            wait(foreground("retro-amber"))
            os.write(master, b"\x11")
            deadline = time.monotonic() + 6
            while child.poll() is None and time.monotonic() < deadline:
                read_once()
            if child.wait(timeout=1) != 0:
                raise AssertionError("unclean foreground exit")
            read_once(0.1)
            if b"\x1b[?1049l" not in output:
                raise AssertionError("alternate screen not restored")
            if (root / "profile" / "credentials" / "session.enc").exists():
                raise AssertionError("offline test created credentials")
            print("PASS:", Path(reader).name, "inherits a live global theme from", Path(writer).name)
        finally:
            if child is not None and child.poll() is None:
                child.terminate()
                try:
                    child.wait(timeout=3)
                except subprocess.TimeoutExpired:
                    child.kill()
                    child.wait()
            os.close(master)
            os.close(slave)


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("--chat", required=True)
    parser.add_argument("--player", required=True)
    options = parser.parse_args()
    chat = str(Path(options.chat).resolve())
    player = str(Path(options.player).resolve())
    run(chat, player, False)
    run(player, chat, True)
