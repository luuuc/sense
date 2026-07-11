# Citation hallucinations

Citations the answer printed that did not resolve against the repo checked out at the benchmarked commit. **Hallucinated** = line number beyond end-of-file (a made-up number). **Unresolved** = file not in the repo, or symbol not within ±5 lines of the cited line.

Reported for transparency; not folded into the headline score.

## baseline

### baseline/netbox  — 726/727 grounded

**Hallucinated**
- `netbox/dcim/models/cables.py:1239` — line 1239 out of range (file only 1238 lines)

## sense

### sense/saleor  — 168/174 grounded

**Unresolved**
- `saleor/checkout/mutations/utils.py:91` — file not found at saleor/checkout/mutations/utils.py
- `saleor/checkout/mutations/checkout_add_promo_code.py:31` — file not found at saleor/checkout/mutations/checkout_add_promo_code.py
- `saleor/checkout/mutations/checkout_billing_address_update.py:24` — file not found at saleor/checkout/mutations/checkout_billing_address_update.py
- `saleor/checkout/mutations/checkout_complete.py:49` — file not found at saleor/checkout/mutations/checkout_complete.py
- `saleor/checkout/mutations/checkout_create.py:211` — file not found at saleor/checkout/mutations/checkout_create.py
- `saleor/checkout/mutations/checkout_remove_promo_code.py:7` — file not found at saleor/checkout/mutations/checkout_remove_promo_code.py

### sense/wagtail  — 206/207 grounded

**Unresolved**
- `wagtail/admin/views/pages/page_privacy.py:15` — file not found at wagtail/admin/views/pages/page_privacy.py
