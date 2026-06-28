# Citation hallucinations

Citations the answer printed that did not resolve against the repo checked out at the benchmarked commit. **Hallucinated** = line number beyond end-of-file (a made-up number). **Unresolved** = file not in the repo, or symbol not within ±5 lines of the cited line.

Reported for transparency; not folded into the headline score.

## baseline

_No ungrounded citations._

## sense

### sense/forem  — 31/32 grounded

**Unresolved**
- `app/models/concerns/cloudinary_helper.rb:1` — file not found at app/models/concerns/cloudinary_helper.rb

### sense/solidus  — 69/70 grounded

**Unresolved**
- `legacy_promotions/app/models/spree/promotion/benefit.rb:341` — file not found at legacy_promotions/app/models/spree/promotion/benefit.rb

### sense/solidus  — 46/48 grounded

**Unresolved**
- `core/app/models/spree/order_contents.rb:6` — file not found at core/app/models/spree/order_contents.rb
- `core/app/models/spree/stock/coordinator.rb:6` — file not found at core/app/models/spree/stock/coordinator.rb
