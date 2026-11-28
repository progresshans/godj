"""Schedule independent Go test processes on bounded Actions workers."""


def pack(weights, capacity):
    if not weights or not 1 <= capacity <= 3 or any(weight <= 0 for weight in weights.values()):
        raise ValueError('invalid worker membership or weights')
    workers = [[] for _ in range((len(weights) + capacity - 1) // capacity)]
    totals = [0] * len(workers)
    for part in sorted(weights, key=lambda part: (-weights[part], part)):
        available = (i for i, parts in enumerate(workers) if len(parts) < capacity)
        index = min(available, key=totals.__getitem__)
        workers[index].append(part)
        totals[index] += weights[part]
    return [parts for _, parts in sorted(zip(totals, workers), key=lambda item: -item[0])]


def verify_outcomes(parts, outcomes):
    if not isinstance(parts, list) or not 1 <= len(parts) <= 3 or len(parts) != len(set(parts)):
        raise ValueError('invalid planned parts: ' + repr(parts))
    expected = ['success'] * len(parts) + ['skipped'] * (3 - len(parts))
    if outcomes != expected:
        raise ValueError(f'worker parts {parts} have outcomes {outcomes}, expected {expected}')
