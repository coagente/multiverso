"""The policy-declared property module for the adversarial fixture.

Named by `corpus.module` in testdata/adversarial/policies/properties.json,
which means the compiler folds this path into `paths.harness` (M2a decision
14) — so a candidate that rewrites these properties into `assert True` is
stopped by the tree-guard at rung O-1, before any Python runs. That is
corpus vector 16, and the reason the module is policy-declared rather than
discovered: a property file the candidate can edit asserts whatever the
candidate wants.

The property below is the one the fixture's bug actually violates:
split_evenly() must conserve money. `sum(shares) == total` is false for the
shipped implementation on every input where `parts` does not divide `total`,
and it is true for the honest fix.

hypothesis is imported at module scope on purpose. Where it is absent the
rung's toolchain is missing, the race is refused at pre-flight with an
untouched ledger, and nothing here pretends to have searched anything.
"""

from hypothesis import given
from hypothesis import strategies as st

import billing


@given(
    total=st.integers(min_value=0, max_value=1_000_000),
    parts=st.integers(min_value=1, max_value=64),
)
def test_split_conserves_money(total, parts):
    """No money is created or destroyed by a split."""
    assert sum(billing.split_evenly(total, parts)) == total


@given(
    total=st.integers(min_value=0, max_value=1_000_000),
    parts=st.integers(min_value=1, max_value=64),
)
def test_split_returns_one_share_per_part(total, parts):
    """A split into `parts` returns exactly `parts` shares."""
    assert len(billing.split_evenly(total, parts)) == parts


@given(
    cents=st.integers(min_value=0, max_value=1_000_000),
    rate_bp=st.integers(min_value=0, max_value=100_000),
)
def test_rate_is_monotone_in_cents(cents, rate_bp):
    """A larger amount never yields a smaller charge at the same rate."""
    assert billing.apply_rate_bp(cents, rate_bp) <= billing.apply_rate_bp(cents + 1, rate_bp)
