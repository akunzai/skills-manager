"""Terminal UI utilities for skills-manager."""

import os
import sys
from typing import List, Optional, Tuple

# ANSI Colors
CYAN = "\033[96m"
GREEN = "\033[92m"
YELLOW = "\033[93m"
RED = "\033[91m"
BOLD = "\033[1m"
DIM = "\033[2m"
RESET = "\033[0m"


def read_key() -> str:
    """Read a single key or ANSI escape sequence from stdin."""
    # Windows support
    try:
        import msvcrt
        ch = msvcrt.getch()
        if ch in (b'\x00', b'\xe0'):
            ch2 = msvcrt.getch()
            if ch2 == b'H':
                return "up"
            if ch2 == b'P':
                return "down"
        if ch in (b'\r', b'\n'):
            return "enter"
        if ch == b' ':
            return "space"
        if ch in (b'\x1b', b'q', b'Q'):
            return "escape"
        if ch in (b'a', b'A'):
            return "a"
        if ch in (b'k', b'K'):
            return "up"
        if ch in (b'j', b'J'):
            return "down"
        if ch == b'\x03':
            return "interrupt"
        return ""
    except ImportError:
        pass

    # Unix/macOS support
    import termios
    import tty
    import select

    fd = sys.stdin.fileno()
    old_settings = termios.tcgetattr(fd)
    try:
        tty.setraw(fd)
        ch = sys.stdin.read(1)
        if ch == '\x1b':
            r, _, _ = select.select([sys.stdin], [], [], 0.05)
            if r:
                ch2 = sys.stdin.read(1)
                if ch2 in ('[', 'O'):
                    ch3 = sys.stdin.read(1)
                    if ch3 == 'A':
                        return "up"
                    if ch3 == 'B':
                        return "down"
            return "escape"
        elif ch in ('\r', '\n'):
            return "enter"
        elif ch == ' ':
            return "space"
        elif ch == '\x03':  # Ctrl+C
            return "interrupt"
        elif ch in ('q', 'Q'):
            return "escape"
        elif ch in ('k', 'K'):
            return "up"
        elif ch in ('j', 'J'):
            return "down"
        elif ch in ('a', 'A'):
            return "a"
        return ch
    finally:
        termios.tcsetattr(fd, termios.TCSADRAIN, old_settings)


def prompt_multi_select(
    title: str,
    items: List[Tuple[str, bool, Optional[str]]],
    initial_checked: Optional[List[bool]] = None
) -> Optional[List[str]]:
    """
    Interactive arrow-key multi-select UI.
    items: list of (name, is_installed, extra_info) tuples.
    By default, only uninstalled skills (is_installed=False) are pre-checked.
    Returns list of selected item names, or None if cancelled.
    """
    if not sys.stdin.isatty():
        return None

    num_items = len(items)
    if num_items == 0:
        return []

    if initial_checked is not None:
        selected = list(initial_checked)
    else:
        # Default: check only skills that are not yet installed
        selected = [not is_inst for (_, is_inst, _) in items]

    cursor_idx = 0

    HIDE_CURSOR = "\033[?25l"
    SHOW_CURSOR = "\033[?25h"
    CLEAR_LINE = "\033[2K"

    def render(first: bool = False) -> None:
        if not first:
            # Move cursor up to overwrite previous render
            # 1 line title + 1 blank line + num_items lines + 1 blank line + 1 instruction line = num_items + 4 lines
            sys.stdout.write(f"\033[{num_items + 4}A\r")

        sys.stdout.write(f"{BOLD}{CYAN}{title}{RESET}\n\n")
        for i, (name, is_installed, extra) in enumerate(items):
            is_cursor = (i == cursor_idx)
            is_checked = selected[i]

            prefix = f"{CYAN}❯{RESET}" if is_cursor else " "
            checkbox = f"{GREEN}[✔]{RESET}" if is_checked else f"{DIM}[ ]{RESET}"

            badge = f" {DIM}(installed){RESET}" if is_installed else ""
            if extra:
                badge += f" {DIM}{extra}{RESET}"

            line = f"{prefix} {checkbox} {BOLD if is_cursor else ''}{name}{RESET}{badge}"
            sys.stdout.write(f"{CLEAR_LINE}{line}\n")

        instructions = f"{DIM}Use ↑/↓ (or k/j) to navigate, Space to toggle, 'a' to toggle all, Enter to confirm, Esc/q to cancel.{RESET}"
        sys.stdout.write(f"\n{CLEAR_LINE}{instructions}\n")
        sys.stdout.flush()

    try:
        sys.stdout.write(HIDE_CURSOR)
        sys.stdout.flush()
        render(first=True)

        while True:
            key = read_key()
            if key == "interrupt":
                raise KeyboardInterrupt()
            elif key == "escape":
                sys.stdout.write("\n")
                sys.stdout.flush()
                return None
            elif key == "enter":
                sys.stdout.write("\n")
                sys.stdout.flush()
                chosen = [items[i][0] for i in range(num_items) if selected[i]]
                return chosen
            elif key == "up":
                cursor_idx = (cursor_idx - 1) % num_items
                render()
            elif key == "down":
                cursor_idx = (cursor_idx + 1) % num_items
                render()
            elif key == "space":
                selected[cursor_idx] = not selected[cursor_idx]
                render()
            elif key == "a":
                all_checked = all(selected)
                selected = [not all_checked] * num_items
                render()
    finally:
        sys.stdout.write(SHOW_CURSOR)
        sys.stdout.flush()
