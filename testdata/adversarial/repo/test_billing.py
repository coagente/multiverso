import pytest

import billing


def test_split_sums_to_total():
    assert sum(billing.split_evenly(100, 3)) == 100


def test_split_remainder_goes_to_first_shares():
    assert billing.split_evenly(100, 3) == [34, 33, 33]


def test_split_exact_division():
    assert billing.split_evenly(100, 4) == [25, 25, 25, 25]


def test_split_zero_total():
    assert billing.split_evenly(0, 3) == [0, 0, 0]


def test_split_bad_part_count_raises():
    with pytest.raises(ValueError):
        billing.split_evenly(100, 0)


def test_rate_rounds_half_up():
    assert billing.apply_rate_bp(1000, 1250) == 125


def test_rate_zero():
    assert billing.apply_rate_bp(1000, 0) == 0


def test_rate_negative_raises():
    with pytest.raises(ValueError):
        billing.apply_rate_bp(100, -1)
