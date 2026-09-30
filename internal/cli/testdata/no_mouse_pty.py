import os, pty, subprocess, select, time, termios, fcntl, struct, sys, tempfile
with tempfile.TemporaryDirectory() as fixture:
 os.makedirs(os.path.join(fixture,'skills','alpha'))
 with open(os.path.join(fixture,'skills','alpha','SKILL.md'),'w') as f: f.write('# Alpha\n')
 master,slave=pty.openpty()
 fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',24,80,0,0))
 env=dict(os.environ,TERM='xterm-256color',HOME=fixture,USERPROFILE=fixture,SKILLS_CLI_MOUSE_FIXTURE=fixture)
 p=subprocess.Popen([sys.argv[1],'-test.run=^TestCLINoMousePTY$'],stdin=slave,stdout=slave,stderr=slave,env=env)
 data=b''
 def until(marker):
  global data
  deadline=time.monotonic()+5
  while marker not in data:
   if time.monotonic()>deadline: raise AssertionError('missing '+repr(marker)+' in '+repr(data))
   if select.select([master],[],[],.1)[0]: data+=os.read(master,65536)
 try:
  until(b'Space to toggle')
  assert b'\x1b[?1000h' not in data and b'\x1b[?1006h' not in data and b'\x1b[6n' not in data, 'mouse enabled despite optout'
  os.write(master,b' \r')
  until(b'[y/N]')
  os.write(master,b'y\n')
  until(b'RESULT:<nil>'); p.wait(timeout=3)
  assert not os.path.exists(os.path.join(fixture,'skills','alpha')), 'keyboard selection did not retire skill'
 finally:
  if p.poll() is None: p.kill(); p.wait()
  os.close(master); os.close(slave)
