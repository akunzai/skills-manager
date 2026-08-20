"""Terminal UI utilities for skills-manager."""

import os
import shutil
import sys
from typing import Dict, List, Optional, Set, Tuple

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
        data = os.read(fd, 32)
        if not data:
            return ""
        if data == b'\x1b':
            # Check if additional escape sequence bytes arrive quickly
            r, _, _ = select.select([fd], [], [], 0.05)
            if r:
                data += os.read(fd, 32)

        if data in (b'\x1b[A', b'\x1bOA', b'\x1b[1;2A', b'\x1b[1;5A'):
            return "up"
        elif data in (b'\x1b[B', b'\x1bOB', b'\x1b[1;2B', b'\x1b[1;5B'):
            return "down"
        elif data in (b'\x1b[C', b'\x1bOC'):
            return "right"
        elif data in (b'\x1b[D', b'\x1bOD'):
            return "left"
        elif data in (b'\r', b'\n'):
            return "enter"
        elif data == b' ':
            return "space"
        elif data == b'\x03':  # Ctrl+C
            return "interrupt"
        elif data in (b'\x1b', b'\x1b\x1b', b'q', b'Q'):
            return "escape"
        elif data in (b'k', b'K'):
            return "up"
        elif data in (b'j', b'J'):
            return "down"
        elif data in (b'a', b'A'):
            return "a"
        return data.decode("utf-8", errors="ignore")
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


def prompt_grouped_multi_select(
    title: str,
    grouped_items: Dict[str, List[Tuple[str, bool, Optional[str]]]],
    initial_checked: Optional[Set[str]] = None
) -> Optional[List[str]]:
    """
    Interactive arrow-key multi-select UI grouped by source.
    grouped_items: {source_name: [(skill_name, is_installed, extra_info), ...]}
    Pressing Space on a group header toggles all skills in that group.
    Returns list of selected skill names, or None if cancelled.
    """
    if not sys.stdin.isatty():
        return None

    # Flatten into rows: either ('group', source, [skill_names]) or ('skill', skill_name, is_installed, extra, source)
    rows: List[Tuple[str, str, Any]] = []
    group_skills_map: Dict[str, List[str]] = {}
    all_skill_names: List[str] = []

    for source, skills in grouped_items.items():
        if not skills:
            continue
        skill_names = [s[0] for s in skills]
        group_skills_map[source] = skill_names
        all_skill_names.extend(skill_names)
        rows.append(("group", source, skill_names))
        for sk_name, is_inst, extra in skills:
            rows.append(("skill", sk_name, (is_inst, extra, source)))

    total_rows = len(rows)
    if total_rows == 0:
        return []

    selected_skills: Set[str] = set(initial_checked) if initial_checked is not None else set()
    cursor_idx = 0
    window_start = 0

    HIDE_CURSOR = "\033[?25l"
    SHOW_CURSOR = "\033[?25h"
    CLEAR_LINE = "\033[2K"

    # Determine scroll window size based on terminal height
    term_rows = shutil.get_terminal_size((80, 24)).lines
    max_visible = max(10, min(term_rows - 6, 20))
    is_scrollable = total_rows > max_visible
    visible_count = min(total_rows, max_visible)

    # Frame height: title (2 lines) + visible rows + (2 scroll indicator lines if scrollable) + instructions (2 lines)
    frame_lines = 2 + visible_count + (2 if is_scrollable else 0) + 2

    def render(first: bool = False) -> None:
        nonlocal window_start
        if not first:
            sys.stdout.write(f"\033[{frame_lines}A\r")

        if is_scrollable:
            if cursor_idx < window_start:
                window_start = cursor_idx
            elif cursor_idx >= window_start + visible_count:
                window_start = cursor_idx - visible_count + 1

        window_end = min(total_rows, window_start + visible_count)

        sys.stdout.write(f"{BOLD}{CYAN}{title}{RESET}\n\n")

        if is_scrollable:
            if window_start > 0:
                sys.stdout.write(f"{CLEAR_LINE}  {DIM}▲ ({window_start} more above){RESET}\n")
            else:
                sys.stdout.write(f"{CLEAR_LINE}\n")

        for i in range(window_start, window_end):
            row_type, name, data = rows[i]
            is_cursor = (i == cursor_idx)
            cursor_str = f"{CYAN}❯{RESET}" if is_cursor else " "

            if row_type == "group":
                g_skills = data
                all_sel = all(s in selected_skills for s in g_skills)
                any_sel = any(s in selected_skills for s in g_skills)

                if all_sel:
                    box = f"{GREEN}[✔]{RESET}"
                elif any_sel:
                    box = f"{YELLOW}[-]{RESET}"
                else:
                    box = f"{DIM}[ ]{RESET}"

                count_str = f"{len(g_skills)} skill" if len(g_skills) == 1 else f"{len(g_skills)} skills"
                line = f"{cursor_str} {box} {BOLD}📦 {name}{RESET} {DIM}({count_str}){RESET}"
                sys.stdout.write(f"{CLEAR_LINE}{line}\n")
            else:
                is_inst, extra, source = data
                is_checked = name in selected_skills
                box = f"{GREEN}[✔]{RESET}" if is_checked else f"{DIM}[ ]{RESET}"
                badge = f" {DIM}(installed){RESET}" if is_inst else ""
                if extra:
                    badge += f" {DIM}{extra}{RESET}"
                line = f"{cursor_str}    {box} {BOLD if is_cursor else ''}{name}{RESET}{badge}"
                sys.stdout.write(f"{CLEAR_LINE}{line}\n")

        if is_scrollable:
            remaining = total_rows - window_end
            if remaining > 0:
                sys.stdout.write(f"{CLEAR_LINE}  {DIM}▼ ({remaining} more below){RESET}\n")
            else:
                sys.stdout.write(f"{CLEAR_LINE}\n")

        instructions = f"{DIM}Use ↑/↓ (or k/j) to navigate, Space to toggle item/group, Enter to confirm, Esc/q to cancel.{RESET}"
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
                chosen = [s for s in all_skill_names if s in selected_skills]
                return chosen
            elif key == "up":
                cursor_idx = (cursor_idx - 1) % total_rows
                render()
            elif key == "down":
                cursor_idx = (cursor_idx + 1) % total_rows
                render()
            elif key == "space":
                row_type, name, data = rows[cursor_idx]
                if row_type == "group":
                    g_skills = data
                    all_sel = all(s in selected_skills for s in g_skills)
                    if all_sel:
                        for s in g_skills:
                            selected_skills.discard(s)
                    else:
                        for s in g_skills:
                            selected_skills.add(s)
                else:
                    if name in selected_skills:
                        selected_skills.remove(name)
                    else:
                        selected_skills.add(name)
                render()
    finally:
        sys.stdout.write(SHOW_CURSOR)
        sys.stdout.flush()
