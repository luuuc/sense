# Citation hallucinations

Citations the answer printed that did not resolve against the repo checked out at the benchmarked commit. **Hallucinated** = line number beyond end-of-file (a made-up number). **Unresolved** = file not in the repo, or symbol not within ±5 lines of the cited line.

Reported for transparency; not folded into the headline score.

## baseline

_No ungrounded citations._

## sense

### sense/discourse  — 8/21 grounded

**Unresolved**
- `app/services/cook_method.rb:78` — file not found at app/services/cook_method.rb
- `app/services/store_upload_service.rb:23` — file not found at app/services/store_upload_service.rb
- `app/services/s3_inventory.rb:56` — file not found at app/services/s3_inventory.rb
- `app/jobs/regular/post_process_upload.rb:34` — file not found at app/jobs/regular/post_process_upload.rb
- `app/jobs/regular/cleanup_uploads.rb:67` — file not found at app/jobs/regular/cleanup_uploads.rb
- `app/services/email_styler.rb:45` — file not found at app/services/email_styler.rb
- `lib/backup_restorer.rb:112` — file not found at lib/backup_restorer.rb
- `lib/backup_restorer/upload_restorer.rb:23` — file not found at lib/backup_restorer/upload_restorer.rb
- `app/services/theme_upload_service.rb:34` — file not found at app/services/theme_upload_service.rb
- `app/services/user_avatar_service.rb:45` — file not found at app/services/user_avatar_service.rb
- `app/services/plugin_upload_service.rb:56` — file not found at app/services/plugin_upload_service.rb
- `app/models/concerns/secure_upload.rb:23` — file not found at app/models/concerns/secure_upload.rb
- `app/services/store_upload_service.rb:45` — file not found at app/services/store_upload_service.rb

### sense/forem  — 31/32 grounded

**Unresolved**
- `app/models/concerns/cloudinary_helper.rb:1` — file not found at app/models/concerns/cloudinary_helper.rb

### sense/gitlabhq  — 26/27 grounded

**Hallucinated**
- `app/models/ability.rb:1245` — line 1245 out of range (file only 211 lines)

### sense/solidus  — 69/70 grounded

**Unresolved**
- `legacy_promotions/app/models/spree/promotion/benefit.rb:341` — file not found at legacy_promotions/app/models/spree/promotion/benefit.rb

### sense/solidus  — 46/48 grounded

**Unresolved**
- `core/app/models/spree/order_contents.rb:6` — file not found at core/app/models/spree/order_contents.rb
- `core/app/models/spree/stock/coordinator.rb:6` — file not found at core/app/models/spree/stock/coordinator.rb
