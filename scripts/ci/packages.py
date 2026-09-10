#!/usr/bin/env python3
"""Partition Go package tests by execution cost and ownership, not test names."""
import argparse
import json
import os
import sys

from scopes import OWNERS

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
PURE_PROTOCOLS = {
    'internal/projectcheck/protocol',
    'internal/projectcheck/migrateprotocol',
    'internal/projectcheck/showmigrationsprotocol',
    'internal/projectcheck/sqlmigrateprotocol',
    'internal/projectcheck/createsuperuserprotocol',
    'internal/projectgenerate/protocol',
    'internal/projectmigration/protocol',
}


# These complete package suites have one owner per OS/arch/mode when the
# relation matrix is selected. CLI/checker subsets are handled separately.
RELATION_PACKAGES = {
    'query', 'codegen/consumertest', 'orm', 'db/sqlite', 'migrations',
    'migrations/definition', 'conformance/migrationrelationproduct',
    'conformance/internal/protocol', 'internal/compiletest',
}
RELATION_PREFIXES = (
    'conformance/relationfixture', 'conformance/relationproduct',
    'conformance/relationqueryproduct', 'conformance/relationobjectproduct',
    'conformance/relationreverseproduct', 'conformance/relationprefetchproduct',
    'conformance/relationselectproduct', 'conformance/relationdeleteproduct',
)
PORTABLE_PRODUCTS = PRODUCTS - {
    'conformance/projectmigratetargetproduct', 'conformance/projectoperatorproduct',
}


def relation_owned(relative):
    return relative in RELATION_PACKAGES or any(relative == prefix or relative.startswith(prefix + '/') for prefix in RELATION_PREFIXES)


def selected(packages, group_name, owners=()):
    if not set(owners) <= set(OWNERS):
        raise ValueError('unknown CI execution owner')
    result = []
    for package in packages:
        category = group(package)
        relative = package[len(MODULE):]
        if group_name == 'relation':
            include = relation_owned(relative)
        elif group_name == 'portable-products':
            include = relative in PORTABLE_PRODUCTS
        else:
            include = group_name == 'all' or category == group_name
        if include and group_name in ('core', 'integration', 'conformance', 'portable-products'):
            if 'relation-product-matrix' in owners and relation_owned(relative):
                include = False
            if 'product-project-check-matrix' in owners and relative == 'conformance/runserverproduct':
                include = False
        if include:
            result.append(package)
    return result


def group(package):
    if not package.startswith(MODULE):
        raise ValueError('package outside GoDj module: ' + package)
    relative = package[len(MODULE):]
    # Wire grammar and resource limits have no OS/process dependency. The
    # linked runner and outer command still exercise them on every platform.
    if relative in PURE_PROTOCOLS:
        return 'core'
    if (relative == 'project' or relative == 'conformance/runners/godj'
            or relative.startswith(('cmd/', 'internal/projectcheck'))):
        return 'platform'
    if relative in PRODUCTS:
        return 'products'
    if relative == 'conformance/relationfixture' or relative.startswith('conformance/relationfixture/'):
        return 'integration'
    if relative.startswith('conformance/'):
        return 'conformance'
    if relative.startswith(('examples/', 'codegen/consumertest', 'internal/projectgenerate', 'internal/compiletest')):
        return 'integration'
    return 'core'


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('group', choices=['core', 'integration', 'conformance', 'products', 'portable-products', 'platform', 'relation', 'all'])
    args = parser.parse_args()
    packages = [line.strip() for line in sys.stdin if line.strip()]
    if not packages or len(packages) != len(set(packages)):
        raise SystemExit('package discovery is empty or duplicated')
    try:
        owners = json.loads(os.environ.get('GODJ_CI_OWNERS', '[]'))
        if not isinstance(owners, list) or any(not isinstance(owner, str) for owner in owners):
            raise ValueError('CI owners must be a list of job names')
        result = selected(packages, args.group, owners)
    except ValueError as error:
        raise SystemExit(str(error)) from error
    if not result:
        raise SystemExit('selected package group is empty')
    print('\n'.join(result))


if __name__ == '__main__':
    main()
