"""The policy-declared property module (M2a decision 14).

This file is named by `corpus.module` in testdata/toyrepo/policies/
properties.json, which means the compiler folds its path into
`paths.harness` — so a candidate that rewrites these properties into
`assert True` is stopped by the tree-guard at rung O-1, before any Python
runs (corpus vector 16). That is the whole reason the module is
policy-declared rather than discovered: a property file the candidate can
edit asserts whatever the candidate wants.

It imports hypothesis at module scope on purpose. Where hypothesis is
absent the collection error is the honest outcome — the rung's toolchain is
missing, the race is refused at pre-flight, and nothing here pretends to
have searched anything.
"""

from hypothesis import assume, given
from hypothesis import strategies as st

import stats


@given(
    value=st.integers(min_value=-1000, max_value=1000),
    low=st.integers(min_value=-1000, max_value=1000),
    high=st.integers(min_value=-1000, max_value=1000),
)
def test_clamp_is_within_bounds(value, low, high):
    """clamp() never returns anything outside [low, high]."""
    assume(low <= high)
    got = stats.clamp(value, low, high)
    assert low <= got <= high


@given(
    value=st.integers(min_value=-1000, max_value=1000),
    low=st.integers(min_value=-1000, max_value=1000),
    high=st.integers(min_value=-1000, max_value=1000),
)
def test_clamp_is_idempotent(value, low, high):
    """Clamping an already-clamped value changes nothing."""
    assume(low <= high)
    once = stats.clamp(value, low, high)
    assert stats.clamp(once, low, high) == once


@given(values=st.lists(st.integers(min_value=-1000, max_value=1000), min_size=1))
def test_total_is_order_independent(values):
    """total() does not depend on the order of its input."""
    assert stats.total(values) == stats.total(list(reversed(values)))
