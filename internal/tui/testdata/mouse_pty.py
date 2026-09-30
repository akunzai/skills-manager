import os, pty, subprocess, select, time, termios, fcntl, struct, sys
master, slave = pty.openpty()
fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack('HHHH', (10 if sys.argv[2]=='wheel' else 12 if sys.argv[2]=='collapse' else 24), 80, 0, 0))
before = termios.tcgetattr(slave)
env = dict(os.environ, TERM='xterm-256color', SKILLS_MOUSE_HELPER='1', SKILLS_MOUSE_SCENARIO=sys.argv[2])
p = subprocess.Popen([sys.argv[1], '-test.run=^TestPromptMousePTY$'], stdin=slave, stdout=slave, stderr=slave, env=env)
data = b''
def until(marker):
 global data
 deadline = time.monotonic()+5
 while marker not in data:
  if time.monotonic()>deadline: raise AssertionError('missing '+repr(marker)+' in '+repr(data))
  if select.select([master], [], [], .1)[0]: data += os.read(master, 65536)
try:
 until(b'\x1b[6n')
 if sys.argv[2]=='timeout':
  until(b'\x1b[?1000l')
  os.write(master,b'j \r')
  until(b'RESULT:[beta]:<nil>')
 elif sys.argv[2]=='wheel': os.write(master,b'\x1b[10;1R')
 elif sys.argv[2]=='collapse': os.write(master,b'\x1b[12;1R')
 else: os.write(master, b'\x1b[24;1R')
 # A 23-line frame ends at row 24, hence beta is row 6.
 if sys.argv[2]=='timeout': pass
 elif sys.argv[2]=='collapse':
  os.write(master,b'\x1b[<0;7;8M'); until(b'child-00')
  data=b''; os.write(master,b'\x1b[<65;10;5M'); until(b'3 more above')
  # The focused fourth header is now at row 5. Collapsing must recenter
  # the five groups, so row 7 selects g2 and blank row 10 does nothing.
  os.write(master,b'\x1b[<0;7;5M\x1b[<0;10;7M\x1b[<0;10;10M\r')
  until(b'RESULT:[g2-skill]:<nil>')
 elif sys.argv[2]=='single':
  os.write(master,b'\x1b[<0;10;6M')
  until('❯ beta'.encode())
  assert b'RESULT:' not in data, 'click committed single selection'
  os.write(master,b'\r'); until(b'RESULT:beta:<nil>')
 elif sys.argv[2]=='wheel':
  os.write(master,b'\x1b[<65;10;5M \r')
  until(b'RESULT:[item-03]:<nil>')
 elif sys.argv[2]=='events':
  # Right/modifier/drag/release and non-list hits must never toggle.
  os.write(master,b'\x1b[<2;10;5M\x1b[<4;10;5M\x1b[<32;10;5M\x1b[<0;10;5m\x1b[<0;10;1M\x1b[<0;10;7M')
  os.write(master,b'\x1b[<0;'); time.sleep(.03); os.write(master,b'10;6M')
  # Multiple coalesced keyboard events survive the same read.
  os.write(master,b'\x1b[A \r'); until(b'RESULT:[alpha beta]:<nil>')
 elif sys.argv[2]=='escape':
  os.write(master,b'\x1b'); until(b'RESULT:[]:<nil>')
 elif sys.argv[2]=='tiny':
  os.write(master,b'\x1b[<0;10;6M'); until('[✓] beta'.encode())
  data=b''; fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',5,15,0,0))
  until(b'Enlarge')
  os.write(master,b' \r'); time.sleep(.1)
  assert b'RESULT:' not in data, 'submitted invisible selection'
  data=b''; fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',12,60,0,0))
  until(b'\x1b[6n'); os.write(master,b'\x1b[12;1R\r')
  until(b'RESULT:[beta]:<nil>')
 elif sys.argv[2] == 'group':
  # Disclosure at column 7 opens the group without selecting it.
  os.write(master,b'\x1b[<0;7;5M')
  until(b'master')
  # Selecting master covers child; clicking child cannot deselect the master.
  os.write(master,b'\x1b[<0;12;6M\x1b[<0;12;7M\r')
  until(b'RESULT:[master]:<nil>')
 elif sys.argv[2] == 'resize':
  os.write(master,b'\x1b[<0;10;6M')
  until('[✓] beta'.encode())
  data=b''
  fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',12,60,0,0))
  until(b'\x1b[6n')
  # Old coordinates must not toggle alpha before the new anchor arrives.
  os.write(master,b'\x1b[<0;10;5M\x1b[12;1R\r')
  until(b'RESULT:[beta]:<nil>')
 else:
  os.write(master, b'\x1b[<0;10;6M\r')
  until(b'RESULT:[beta]:<nil>')
 p.wait(timeout=3)
 after = termios.tcgetattr(slave)
 # Darwin EXTPROC is kernel-owned/read-only in tcsetattr, not a raw mode flag.
 # https://github.com/apple-oss-distributions/xnu/blob/main/bsd/kern/tty.c
 if sys.platform == 'darwin':
  before[3] &= ~0x20000000; after[3] &= ~0x20000000
 assert before == after, 'terminal mode leaked'
 assert b'\x1b[?1000l' in data and b'\x1b[?1006l' in data, repr(data)
finally:
 if p.poll() is None: p.kill(); p.wait()
 os.close(master); os.close(slave)
