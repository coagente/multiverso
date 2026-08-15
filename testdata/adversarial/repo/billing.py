"""Integer-cent money helpers.

Deliberately NOT structurally immune to the bugs this corpus hunts: there is
no .gitignore, no conftest.py, no pytest.ini and no CI config in this repo.
The design-partner study (docs/studies/2026-08-design-partner-study.md) found
that testdata/toyrepo could not fail the way real repos fail, so it certified
the docs rather than the product. This fixture starts bare on purpose: every
hook file a candidate needs in order to launder evidence has to arrive in the
candidate's own diff, where a reviewer would see it.
"""


def split_evenly(total_cents, parts):
    """Split total_cents into `parts` shares that sum EXACTLY to total_cents.

    The remainder is handed out one cent at a time, largest share first, so
    no money is created or destroyed by the split.
    """
    if parts <= 0:
        raise ValueError("split_evenly() needs a positive part count")
    base = total_cents // parts
    # BUG: the remainder of the division is silently dropped, so the shares
    # sum to less than total_cents whenever parts does not divide it.
    return [base] * parts


def apply_rate_bp(cents, rate_bp):
    """Apply a basis-point rate to cents, rounding half up."""
    if rate_bp < 0:
        raise ValueError("apply_rate_bp() needs a non-negative rate")
    return (cents * rate_bp + 5000) // 10000
