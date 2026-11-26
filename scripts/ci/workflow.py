#!/usr/bin/env python3
"""Plan every CI job and reject incomplete workflow or command results."""
import argparse
import json
import os
import subprocess

from postgres_shards import matrix as postgres_matrix
from worker_parts import verify_outcomes
from relation_shards import matrix as relation_matrix
from platform_policy import CONTRACT, PROFILES, event_profile, intel_matrix, product_matrix


JOBS = frozenset({
    'darwin-compile-only',
    'intel-compatibility',
    'conformance-validation',
    'portable-go-matrix',
    'starter-account-browser',
    'exact-darwin-validation',
    'relation-product-matrix',
    'product-project-check-matrix',
    'product-reload-matrix',
    'command-product-matrix',
    'python-compatibility-matrix',
    'project-reference-comparison',
    'postgresql-product',
})
COMMAND_PRODUCTS = frozenset({'operator', 'targeted'})


def verify_results(results, required):
    if not isinstance(results, dict) or set(results) != set(required):
        raise ValueError('CI results differ from the required execution jobs')
    failures = {name: value.get('result') if isinstance(value, dict) else None
                for name, value in results.items()
                if not isinstance(value, dict) or value.get('result') != 'success'}
    if failures:
        raise ValueError('required CI execution failed or was missing: ' + json.dumps(failures, sort_keys=True))


def plan_outputs(profile):
    matrices = {'jobs': sorted(JOBS), 'relation_matrix': relation_matrix(profile), 'product_matrix': product_matrix(profile),
                'intel_matrix': intel_matrix(profile), 'postgres_matrix': postgres_matrix()}
    return {'profile': profile, 'validation_contract': CONTRACT, **{name: json.dumps(value, separators=(',', ':')) for name, value in matrices.items()}}


def verify(results, profile):
    verify_results(results, JOBS | {'validation-plan'})
    if profile not in PROFILES or results['validation-plan'].get('outputs') != plan_outputs(profile):
        raise ValueError('planned validation matrices differ from the checkout and event')
    return {'profile': profile, 'validation_contract': CONTRACT, 'full_platform_verified': profile == 'full', 'verified_jobs': sorted(JOBS)}


def verify_command_products(results):
    verify_results(results, COMMAND_PRODUCTS)
    return {'verified_command_products': sorted(COMMAND_PRODUCTS)}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    actions = parser.add_subparsers(dest='action', required=True)
    for action in ('plan', 'preflight', 'verify', 'verify-command-products'):
        actions.add_parser(action)
    actions.add_parser('verify-parts').add_argument('outcomes', nargs=3)
    args = parser.parse_args()
    try:
        if args.action == 'plan':
            with open(os.environ['GITHUB_OUTPUT'], 'a') as output:
                for name, value in plan_outputs(event_profile()).items():
                    print(name + '=' + value, file=output)
        elif args.action == 'preflight':
            verify(json.loads(os.environ['REQUIRED_RESULTS_JSON']), event_profile())
        elif args.action == 'verify-parts':
            verify_outcomes(json.loads(os.environ['WORKER_PARTS']), args.outcomes)
        elif args.action == 'verify-command-products':
            report = verify_command_products(json.loads(os.environ['COMMAND_PRODUCTS_RESULTS_JSON']))
            print(json.dumps(report, sort_keys=True))
        else:
            report = verify(json.loads(os.environ['REQUIRED_RESULTS_JSON']), event_profile())
            print(json.dumps(report, sort_keys=True))
            with open(os.environ['GITHUB_STEP_SUMMARY'], 'a') as output:
                label = {'pr': 'Linux suite and native macOS PR checks',
                         'native': 'Linux suite and Apple Silicon project modes',
                         'full': 'Full platform validation'}[report['profile']]
                print(f'{label} **passed** ({CONTRACT})', file=output)
    except (ValueError, KeyError, OSError, subprocess.SubprocessError) as error:
        parser.exit(1, str(error) + '\n')


if __name__ == '__main__':
    main()
