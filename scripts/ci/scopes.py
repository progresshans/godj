#!/usr/bin/env python3
"""Select CI owners and verify exactly the requested validation scope."""
import argparse
import json
import os


SCOPES = {'full', 'orm', 'cli', 'web', 'reference'}
COMMAND_PRODUCTS = {
    'operator': {'cli', 'web'},
    'targeted': {'cli', 'orm'},
}
OWNERS = {
    'conformance-validation': {'reference'},
    'portable-go-matrix': {'orm', 'cli', 'web'},
    'exact-darwin-validation': {'reference'},
    'relation-product-matrix': {'orm'},
    'product-project-check-matrix': {'cli'},
    'command-product-matrix': set().union(*COMMAND_PRODUCTS.values()),
    'python-compatibility-matrix': {'reference'},
    'postgresql-product': {'orm', 'cli', 'web', 'reference'},
}


def selected(suite):
    suite = suite.removeprefix('ci:')
    if suite not in SCOPES:
        raise ValueError('unknown validation scope')
    return suite, sorted(name for name, owners in OWNERS.items() if suite == 'full' or suite in owners)


def verify(suite, results):
    suite, jobs = selected(suite)
    verify_results(results, OWNERS, jobs)
    return {'scope': suite, 'full_platform_verified': suite == 'full', 'verified_jobs': jobs}


def command_products(suite):
    suite, _ = selected(suite)
    return sorted(name for name, owners in COMMAND_PRODUCTS.items() if suite == 'full' or suite in owners)


def verify_command_products(suite, results):
    products = command_products(suite)
    verify_results(results, COMMAND_PRODUCTS, products)
    return {'verified_command_products': products}


def verify_results(results, owners, selected_owners):
    if set(results) != set(owners):
        raise ValueError('CI job roster differs from declared owners')
    failures = {name: value.get('result') for name, value in results.items()
                if value.get('result') != ('success' if name in selected_owners else 'skipped')}
    if failures:
        raise ValueError('selected CI scope failed or execution was missing: ' + json.dumps(failures, sort_keys=True))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('action', choices=['plan', 'verify', 'verify-command-products'])
    args = parser.parse_args()
    try:
        suite, jobs = selected(os.environ['VALIDATION_SUITE'])
        if args.action == 'plan':
            with open(os.environ['GITHUB_OUTPUT'], 'a') as output:
                print('suite=' + suite, file=output)
                print('jobs=' + json.dumps(jobs), file=output)
                print('command_products=' + json.dumps(command_products(suite)), file=output)
        elif args.action == 'verify-command-products':
            report = verify_command_products(suite, json.loads(os.environ['COMMAND_PRODUCTS_RESULTS_JSON']))
            print(json.dumps(report, sort_keys=True))
        else:
            results = json.loads(os.environ['REQUIRED_RESULTS_JSON'])
            plan = results.pop('validation-plan', {})
            if plan.get('result') != 'success':
                raise ValueError('validation plan did not succeed')
            report = verify(suite, results)
            print(json.dumps(report, sort_keys=True))
            with open(os.environ['GITHUB_STEP_SUMMARY'], 'a') as output:
                print('Verified scope: **' + suite + '**', file=output)
                print('\nFull platform validation: ' + ('passed' if report['full_platform_verified'] else 'not requested'), file=output)
    except (ValueError, KeyError, OSError) as error:
        parser.exit(1, str(error) + '\n')


if __name__ == '__main__':
    main()
