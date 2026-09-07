#!/usr/bin/env python3
"""Partition Go package tests by execution cost and ownership, not test names."""
import argparse
import sys

MODULE = 'github.com/progresshans/godj/'
PRODUCTS = {
    'conformance/projectmigrateproduct',
    'conformance/projectmigratetargetproduct',
    'conformance/projectshowmigrationsproduct',
    'conformance/projectsqlmigrateproduct',
    'conformance/projectoperatorproduct',
    'conformance/runserverproduct',
    'conformance/migrationwriterproduct',
}


def group(package):
    if not package.startswith(MODULE):
        raise ValueError('package outside GoDj module: ' + package)
    relative = package[len(MODULE):]
    if (relative == 'project' or relative == 'conformance/runners/godj'
            or relative.startswith(('cmd/', 'internal/projectcheck'))):
        return 'platform'
    if relative in PRODUCTS:
        return 'products'
    if relative.startswith('conformance/'):
        return 'conformance'
    if relative.startswith(('examples/', 'codegen/consumertest', 'internal/projectgenerate', 'internal/compiletest')):
        return 'integration'
    return 'core'


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('group', choices=['core', 'integration', 'conformance', 'products', 'platform', 'all'])
    args = parser.parse_args()
    packages = [line.strip() for line in sys.stdin if line.strip()]
    if not packages or len(packages) != len(set(packages)):
        raise SystemExit('package discovery is empty or duplicated')
    selected = [package for package in packages if args.group == 'all' or group(package) == args.group]
    if not selected:
        raise SystemExit('selected package group is empty')
    print('\n'.join(selected))


if __name__ == '__main__':
    main()
