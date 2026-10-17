#!/usr/bin/env python3
"""Plan every CI job and reject incomplete workflow or command results."""
import argparse
import json
import os

from relation_shards import matrix as relation_matrix


JOBS = frozenset({
    'conformance-validation',
    'portable-go-matrix',
    'exact-darwin-validation',
    'relation-product-matrix',
    'product-project-check-matrix',
    'command-product-matrix',
    'python-compatibility-matrix',
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


def verify(results):
    verify_results(results, JOBS | {'validation-plan'})
    return {'full_platform_verified': True, 'verified_jobs': sorted(JOBS)}


def verify_command_products(results):
    verify_results(results, COMMAND_PRODUCTS)
    return {'verified_command_products': sorted(COMMAND_PRODUCTS)}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('action', choices=['plan', 'verify', 'verify-command-products'])
    args = parser.parse_args()
    try:
        if args.action == 'plan':
            with open(os.environ['GITHUB_OUTPUT'], 'a') as output:
                print('jobs=' + json.dumps(sorted(JOBS)), file=output)
                print('relation_matrix=' + json.dumps(relation_matrix()), file=output)
        elif args.action == 'verify-command-products':
            report = verify_command_products(json.loads(os.environ['COMMAND_PRODUCTS_RESULTS_JSON']))
            print(json.dumps(report, sort_keys=True))
        else:
            report = verify(json.loads(os.environ['REQUIRED_RESULTS_JSON']))
            print(json.dumps(report, sort_keys=True))
            with open(os.environ['GITHUB_STEP_SUMMARY'], 'a') as output:
                print('Full platform validation: **passed**', file=output)
    except (ValueError, KeyError, OSError) as error:
        parser.exit(1, str(error) + '\n')


if __name__ == '__main__':
    main()
