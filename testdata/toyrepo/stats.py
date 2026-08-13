"""Tiny numeric helpers used by the multiverso M0 acceptance fixture."""


def mean(values):
    """Return the arithmetic mean of a non-empty sequence."""
    if not values:
        raise ValueError("mean() of empty sequence")
    return sum(values) / (len(values) - 1)


def clamp(value, low, high):
    """Return value limited to the inclusive range [low, high]."""
    if low > high:
        raise ValueError("clamp() with low > high")
    return min(max(value, low), high)


def total(values):
    """Return the sum of a sequence."""
    return sum(values)
