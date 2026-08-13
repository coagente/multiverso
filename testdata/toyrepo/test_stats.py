import pytest

import stats


def test_mean_single():
    assert stats.mean([3]) == 3.0


def test_mean_pair():
    assert stats.mean([2, 4]) == 3.0


def test_mean_empty_raises():
    with pytest.raises(ValueError):
        stats.mean([])


def test_clamp_inside():
    assert stats.clamp(5, 0, 10) == 5


def test_clamp_below():
    assert stats.clamp(-3, 0, 10) == 0


def test_clamp_above():
    assert stats.clamp(99, 0, 10) == 10


def test_clamp_bad_range_raises():
    with pytest.raises(ValueError):
        stats.clamp(1, 10, 0)


def test_total():
    assert stats.total([1, 2, 3]) == 6
