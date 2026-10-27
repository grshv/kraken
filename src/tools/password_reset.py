#!/usr/bin/env python3
"""Local reset helper: hidden input, stdin only, no credential files."""
import getpass
from pathlib import Path
import subprocess

root = Path(__file__).resolve().parents[2]
username = input('Имя администратора [admin]: ').strip() or 'admin'
password = getpass.getpass('Новый пароль (не менее 12 байт): ')
if password != getpass.getpass('Повторите пароль: '):
    raise SystemExit('Пароли не совпадают')
if not 12 <= len(password.encode()) <= 1024:
    raise SystemExit('Пароль должен занимать 12–1024 байта UTF-8')
result = subprocess.run([
    'docker', 'compose', '-f', str(root/'deploy/compose.yaml'), 'run', '--rm', '--no-deps',
    '-T', '--user', '13:13', '--entrypoint', '/usr/local/bin/squid-control',
    'squid-control', 'password-reset', '--username', username, '--password-stdin',
], input=password+'\n', text=True)
raise SystemExit(result.returncode)
