# 在联网沙箱内运行：经代理访问宿主回环、localhost 与链路本地地址都应被拒绝（403），
# 不经代理直连宿主端口应失败，Unix socket 仍被禁止；NX_TEST_INTERNET=1 时再经代理访问一次公网 HTTPS。
import os
import socket
import sys

port = int(sys.argv[1])
proxy = os.environ['HTTPS_PROXY']
assert proxy.startswith('http://127.0.0.1:'), proxy
proxy_port = int(proxy.rsplit(':', 1)[1])


def ask(line):
    s = socket.create_connection(('127.0.0.1', proxy_port), timeout=15)
    s.sendall(line.encode())
    data = b''
    while True:
        chunk = s.recv(4096)
        if not chunk:
            break
        data += chunk
    s.close()
    return data.decode('utf-8', 'replace')


for line in [
    'CONNECT 127.0.0.1:%d HTTP/1.1\r\nHost: x\r\n\r\n' % port,
    'GET http://127.0.0.1:%d/ HTTP/1.1\r\nHost: 127.0.0.1\r\n\r\n' % port,
    'CONNECT localhost:%d HTTP/1.1\r\nHost: x\r\n\r\n' % port,
    'GET http://169.254.169.254/ HTTP/1.1\r\nHost: 169.254.169.254\r\n\r\n',
]:
    reply = ask(line)
    if ' 403 ' not in reply.split('\r\n')[0] or 'host-secret' in reply:
        print('LEAK', repr(line), repr(reply))
        sys.exit(1)

try:
    socket.create_connection(('127.0.0.1', port), timeout=3)
    print('DIRECT')
    sys.exit(1)
except OSError:
    pass

try:
    socket.socket(socket.AF_UNIX)
    print('UNIX')
    sys.exit(1)
except PermissionError:
    pass

if os.environ.get('NX_TEST_INTERNET') == '1':
    import urllib.request
    with urllib.request.urlopen('https://example.com/', timeout=30) as response:
        assert response.status == 200, response.status

print('NET-OK')
