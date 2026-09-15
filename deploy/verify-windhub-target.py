"""Read-only WindHub release gate. Requires existing paramiko and SSH config."""
import argparse
import json
from pathlib import Path
import re
import socket
import subprocess
import paramiko


def blocks(text, header):
    # Read balanced nginx blocks; unknown layouts fail closed in validate().
    for match in re.finditer(header + r"\s*\{", text):
        depth = 1
        for end in range(match.end(), len(text)):
            depth += (text[end] == '{') - (text[end] == '}')
            if depth == 0:
                yield text[match.end():end]
                break


def validate(hostname, addresses, expected_ip, image, expected_id, commit, nginx):
    checks = {
        'host': hostname.strip() == 'wxm-tenant-platform-hz-01',
        'dns': set(addresses) == {expected_ip},
        'image': image['Image'] == expected_id,
        'source_tag': image['Config']['Image'].endswith('-' + commit[:9]),
        'gateway_map': any(re.search(r'windhub\.online\s+http://sub2api:8080;', b)
                           for b in blocks(nginx, r'map\s+\$host\s+\$backend_url')),
        'gateway_vhost': any(re.search(r'server_name\s+windhub\.online;', b)
                             and 'proxy_pass $backend_url;' in b
                             for b in blocks(nginx, r'\bserver')),
    }
    if not all(checks.values()):
        raise RuntimeError('Target mismatch: ' + ', '.join(k for k, ok in checks.items() if not ok))
    return checks


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--commit', required=True)
    p.add_argument('--image-id', required=True, help='Independently verified immutable production image SHA256')
    args = p.parse_args()
    commit = subprocess.check_output(['git', 'rev-parse', '--verify', args.commit + '^{commit}'], text=True).strip()
    config = paramiko.SSHConfig()
    with (Path.home() / '.ssh/config').open() as f:
        config.parse(f)
    target = config.lookup('wxm-tenant-platform-hz-01')
    addresses = {item[4][0] for item in socket.getaddrinfo('windhub.online', 443, type=socket.SOCK_STREAM)}
    with paramiko.SSHClient() as client:
        client.load_system_host_keys()
        client.set_missing_host_key_policy(paramiko.RejectPolicy())
        client.connect(target['hostname'], port=int(target['port']), username=target['user'], key_filename=target['identityfile'], timeout=15)
        def read(command):
            _, out, err = client.exec_command(command, timeout=30)
            value = out.read().decode()
            if out.channel.recv_exit_status():
                raise RuntimeError('Read-only remote probe failed')
            return value
        hostname = read('hostname')
        # Select only identity fields; never print container environment/secrets.
        image = json.loads(read("docker inspect sub2api --format '{\"Image\":{{json .Image}},\"Config\":{\"Image\":{{json .Config.Image}}}}'"))
        nginx = read('docker exec wxm-public-gateway nginx -T 2>/dev/null')
        checks = validate(hostname, addresses, target['hostname'], image, args.image_id, commit, nginx)
    print(json.dumps({'checks': checks, 'source_commit': commit, 'image_id': args.image_id}))
    print('Source linkage uses the verified image ID plus its tag; this is not a build attestation.')


if __name__ == '__main__':
    main()
