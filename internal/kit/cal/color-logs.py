#!/usr/bin/env python3
"""Follow logs, retaining ANSI colours and highlighting older plain output."""
import os, re, subprocess, sys

def colour(line):
    if '\x1b[' in line:
        return line
    rules = [(r'\b(error|failed|failure|fatal)\b|\b[45]\d\d\b',31),
             (r'\b(warn|warning)\b|YN0002|YN0086',33),
             (r'\b(ready|success|successful|completed)\b|✓|\b20[0-9]\b',32),
             (r'\b(info|starting|compiling)\b',36)]
    for pattern, code in rules:
        if re.search(pattern,line,re.I):
            return f'\x1b[{code}m{line.rstrip()}\x1b[0m\n'
    return line

if __name__ == '__main__':
    child=subprocess.Popen(sys.argv[1:],stdout=subprocess.PIPE,text=True,errors='replace',env=dict(os.environ,SYSTEMD_COLORS='1'))
    try:
        for line in child.stdout:
            sys.stdout.write(colour(line));sys.stdout.flush()
        sys.exit(child.wait())
    except (KeyboardInterrupt,BrokenPipeError):
        child.terminate();child.wait()
