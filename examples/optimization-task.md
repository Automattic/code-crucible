# Example Optimization Task

Optimize checkout price calculation.

## Goal

Reduce p95 latency while preserving exact output compatibility for all supported carts, coupons, shipping methods, and tax settings.

## Candidate Boundary

Competitors must expose the same public function or command as the baseline implementation. Caller code should not change.

## Correctness

Every competitor must pass:

- Golden cart fixtures
- Invalid coupon cases
- Empty cart case
- High item-count case
- Multi-currency case
- Tax rounding regression cases

## Metrics

Primary metric:

- p95 latency

Constraints:

- Correctness must pass
- Peak memory must not exceed baseline by more than 10 percent
- External call count must be less than or equal to baseline

## External Policy

Use `replay` mode for payment, tax, and shipping API calls.
