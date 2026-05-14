# Citation hallucinations

Citations the assistant printed that did not resolve against the repo checked out at `run_meta.repo_commit`. **Hallucinated** = line number beyond EOF (made-up number). **Unresolved** = file not in repo, or symbol not within ±5 lines of the cited line.

Not yet folded into the fairness score — see pitch 20-04.

## baseline

### baseline/axum  — 60/65 grounded

**Unresolved**
- `path_router.rs:347` — file not found at path_router.rs
- `path_router.rs:334` — file not found at path_router.rs
- `serve/mod.rs:333` — file not found at serve/mod.rs
- `matched_path.rs:101` — file not found at matched_path.rs
- `extract/matched_path.rs:67` — file not found at extract/matched_path.rs

### baseline/discourse  — 50/54 grounded

**Unresolved**
- `topic_view_details_serializer.rb:13` — file not found at topic_view_details_serializer.rb
- `post_creator.rb:183` — file not found at post_creator.rb
- `posts_controller.rb:891` — file not found at posts_controller.rb
- `new_post_manager.rb:244` — file not found at new_post_manager.rb

### baseline/flask  — 9/17 grounded

**Unresolved**
- `app.py:1625` — file not found at app.py
- `app.py:992` — file not found at app.py
- `app.py:1366` — file not found at app.py
- `app.py:966` — file not found at app.py
- `app.py:1021` — file not found at app.py
- `app.py:1224` — file not found at app.py
- `app.py:1394` — file not found at app.py
- `app.py:1577` — file not found at app.py

### baseline/javalin  — 19/57 grounded

**Unresolved**
- `JavalinDefaultRoutingApi.kt:104` — file not found at JavalinDefaultRoutingApi.kt
- `config/RoutesConfig.kt:34` — file not found at config/RoutesConfig.kt
- `router/InternalRouter.kt:42` — file not found at router/InternalRouter.kt
- `matcher/PathMatcher.kt:20` — file not found at matcher/PathMatcher.kt
- `JavalinServlet.kt:56` — file not found at JavalinServlet.kt
- `config/JavalinState.kt:83` — file not found at config/JavalinState.kt
- `servlet/DefaultTasks.kt:16` — file not found at servlet/DefaultTasks.kt
- `DefaultTasks.kt:22` — file not found at DefaultTasks.kt
- `DefaultTasks.kt:44` — file not found at DefaultTasks.kt
- `InternalRouter.kt:77` — file not found at InternalRouter.kt
- `matcher/PathMatcher.kt:50` — file not found at matcher/PathMatcher.kt
- `ParsedEndpoint.kt:14` — file not found at ParsedEndpoint.kt
- `Endpoint.kt:78` — file not found at Endpoint.kt
- `DefaultTasks.kt:75` — file not found at DefaultTasks.kt
- `DefaultTasks.kt:84` — file not found at DefaultTasks.kt
- `InternalRouter.kt:93` — file not found at InternalRouter.kt
- `error/ErrorMapper.kt:23` — file not found at error/ErrorMapper.kt
- `DefaultTasks.kt:88` — file not found at DefaultTasks.kt
- `InternalRouter.kt:109` — file not found at InternalRouter.kt
- `exception/ExceptionMapper.kt:34` — file not found at exception/ExceptionMapper.kt
- `config/JavalinState.kt:77` — file not found at config/JavalinState.kt
- `InternalRouter.kt:115` — file not found at InternalRouter.kt
- `exception/ExceptionMapper.kt:60` — file not found at exception/ExceptionMapper.kt
- `JavalinDefaultRoutingApi.kt:79` — file not found at JavalinDefaultRoutingApi.kt
- `InternalRouter.kt:44` — file not found at InternalRouter.kt
- `PathMatcher.kt:15` — file not found at PathMatcher.kt
- `JavalinDefaultRoutingApi.kt:51` — file not found at JavalinDefaultRoutingApi.kt
- `RoutesConfig.kt:30` — file not found at RoutesConfig.kt
- `InternalRouter.kt:85` — file not found at InternalRouter.kt
- `JavalinDefaultRoutingApi.kt:44` — file not found at JavalinDefaultRoutingApi.kt
- `RoutesConfig.kt:26` — file not found at RoutesConfig.kt
- `InternalRouter.kt:100` — file not found at InternalRouter.kt
- `InternalRouter.kt:30` — file not found at InternalRouter.kt
- `ExceptionMapper.kt:24` — file not found at ExceptionMapper.kt
- `DefaultTasks.kt:18` — file not found at DefaultTasks.kt
- `JavalinServlet.kt:112` — file not found at JavalinServlet.kt
- `DefaultTasks.kt:37` — file not found at DefaultTasks.kt
- `DefaultTasks.kt:79` — file not found at DefaultTasks.kt

### baseline/nextjs  — 23/24 grounded

**Unresolved**
- `base-server.ts:894` — file not found at base-server.ts

## gitnexus

### gitnexus/axum  — 58/75 grounded

**Unresolved**
- `mod.rs:217` — file not found at mod.rs
- `mod.rs:255` — file not found at mod.rs
- `mod.rs:278` — file not found at mod.rs
- `mod.rs:53` — file not found at mod.rs
- `mod.rs:79` — file not found at mod.rs
- `mod.rs:31` — file not found at mod.rs
- `mod.rs:91` — file not found at mod.rs
- `mod.rs:21` — file not found at mod.rs
- `mod.rs:107` — file not found at mod.rs
- `handler/mod.rs:189` — file not found at handler/mod.rs
- `path_router.rs:332` — file not found at path_router.rs
- `matched_path.rs:67` — file not found at matched_path.rs
- `original_uri.rs:70` — file not found at original_uri.rs
- `matched_path.rs:84` — file not found at matched_path.rs
- `handler/mod.rs:208` — file not found at handler/mod.rs
- `method_routing.rs:1355` — file not found at method_routing.rs
- `handler/service.rs:165` — file not found at handler/service.rs

### gitnexus/discourse  — 38/55 grounded

**Unresolved**
- `posts_controller.rb:891` — file not found at posts_controller.rb
- `posts_controller.rb:189` — file not found at posts_controller.rb
- `posts_controller.rb:194` — file not found at posts_controller.rb
- `topics_controller.rb:996` — file not found at topics_controller.rb
- `posts_controller.rb:183` — file not found at posts_controller.rb
- `new_post_manager.rb:228` — file not found at new_post_manager.rb
- `new_post_manager.rb:158` — file not found at new_post_manager.rb
- `post_creator.rb:198` — file not found at post_creator.rb
- `post_creator.rb:96` — file not found at post_creator.rb
- `post_creator.rb:200` — file not found at post_creator.rb
- `post_creator.rb:505` — file not found at post_creator.rb
- `topic_guardian.rb:46` — file not found at topic_guardian.rb
- `new_post_manager.rb:173` — file not found at new_post_manager.rb
- `current_user_serializer.rb:140` — file not found at current_user_serializer.rb
- `groups_controller.rb:516` — file not found at groups_controller.rb
- `post_guardian.rb:147` — file not found at post_guardian.rb
- `topic_guardian.rb:49` — file not found at topic_guardian.rb

### gitnexus/javalin  — 0/6 grounded

**Unresolved**
- `ExceptionMapper.kt:registered` — file not found at ExceptionMapper.kt
- `JavalinServlet.kt:existing` — file not found at JavalinServlet.kt
- `InternalRouter.kt:addHttpExceptionHandler` — file not found at InternalRouter.kt
- `DefaultTasks.kt:90` — file not found at DefaultTasks.kt
- `ExceptionMapper.kt:38` — file not found at ExceptionMapper.kt
- `TestExceptionMapper.kt:register` — file not found at TestExceptionMapper.kt

### gitnexus/nextjs  — 9/26 grounded

**Unresolved**
- `next.ts:106` — file not found at next.ts
- `base-server.ts:1974` — file not found at base-server.ts
- `next-server.ts:635` — file not found at next-server.ts
- `route-modules/app-page/module.render.ts:3` — file not found at route-modules/app-page/module.render.ts
- `app-render/app-render.tsx:3077` — file not found at app-render/app-render.tsx
- `next-server.ts:658` — file not found at next-server.ts
- `render.tsx:1621` — file not found at render.tsx
- `render.tsx:457` — file not found at render.tsx
- `load-components.ts:355` — file not found at load-components.ts
- `next-server.ts:804` — file not found at next-server.ts
- `module.ts:155` — file not found at module.ts
- `app-render.tsx:3077` — file not found at app-render.tsx
- `module.ts:141` — file not found at module.ts
- `next-server.ts:532` — file not found at next-server.ts
- `send-payload.ts:34` — file not found at send-payload.ts
- `router-server.ts:370` — file not found at router-server.ts
- `base-server.ts:880` — file not found at base-server.ts

## probe

### probe/axum  — 11/15 grounded

**Unresolved**
- `service.rs:171` — file not found at service.rs
- `routing/method_routing.rs:1355` — file not found at routing/method_routing.rs
- `or.rs:56` — file not found at or.rs
- `mod.rs:138` — file not found at mod.rs

### probe/discourse  — 56/57 grounded

**Unresolved**
- `current_user_serializer.rb:140` — file not found at current_user_serializer.rb

### probe/javalin  — 28/65 grounded

**Unresolved**
- `JavalinDefaultRoutingApi.kt:104` — file not found at JavalinDefaultRoutingApi.kt
- `RoutesConfig.kt:34` — file not found at RoutesConfig.kt
- `InternalRouter.kt:42` — file not found at InternalRouter.kt
- `PathMatcher.kt:20` — file not found at PathMatcher.kt
- `JavalinServlet.kt:37` — file not found at JavalinServlet.kt
- `JavalinState.kt:83` — file not found at JavalinState.kt
- `JavalinServlet.kt:43` — file not found at JavalinServlet.kt
- `JavalinServlet.kt:56` — file not found at JavalinServlet.kt
- `DefaultTasks.kt:16` — file not found at DefaultTasks.kt
- `DefaultTasks.kt:22` — file not found at DefaultTasks.kt
- `DefaultTasks.kt:52` — file not found at DefaultTasks.kt
- `Endpoint.kt:37` — file not found at Endpoint.kt
- `DefaultTasks.kt:62` — file not found at DefaultTasks.kt
- `DefaultTasks.kt:69` — file not found at DefaultTasks.kt
- `DefaultTasks.kt:75` — file not found at DefaultTasks.kt
- `DefaultTasks.kt:88` — file not found at DefaultTasks.kt
- `DefaultTasks.kt:84` — file not found at DefaultTasks.kt
- `InternalRouter.kt:93` — file not found at InternalRouter.kt
- `JavalinServlet.kt:106` — file not found at JavalinServlet.kt
- `InternalRouter.kt:109` — file not found at InternalRouter.kt
- `ExceptionMapper.kt:55` — file not found at ExceptionMapper.kt
- `JavalinServlet.kt:61` — file not found at JavalinServlet.kt
- `ExceptionMapper.kt:60` — file not found at ExceptionMapper.kt
- `JavalinServlet.kt:80` — file not found at JavalinServlet.kt
- `JavalinServlet.kt:96` — file not found at JavalinServlet.kt
- `JavalinServlet.kt:116` — file not found at JavalinServlet.kt
- `JavalinServlet.kt:120` — file not found at JavalinServlet.kt
- `JavalinServlet.kt:124` — file not found at JavalinServlet.kt
- `ExceptionMapper.kt:48` — file not found at ExceptionMapper.kt
- `JavalinDefaultRoutingApi.kt:44` — file not found at JavalinDefaultRoutingApi.kt
- `InternalRouter.kt:85` — file not found at InternalRouter.kt
- `DefaultTasks.kt:18` — file not found at DefaultTasks.kt
- `DefaultTasks.kt:37` — file not found at DefaultTasks.kt
- `DefaultTasks.kt:79` — file not found at DefaultTasks.kt
- `DefaultTasks.kt:90` — file not found at DefaultTasks.kt
- `DefaultTasks.kt:85` — file not found at DefaultTasks.kt
- `TestErrorMapper.kt:42` — file not found at TestErrorMapper.kt

### probe/nextjs  — 15/40 grounded

**Unresolved**
- `base-server.ts:877` — file not found at base-server.ts
- `base-server.ts:2043` — file not found at base-server.ts
- `next-server.ts:604` — file not found at next-server.ts
- `route-modules/app-page/module.render.ts:3` — file not found at route-modules/app-page/module.render.ts
- `route-modules/app-page/module.ts:155` — file not found at route-modules/app-page/module.ts
- `app-render/app-render.tsx:3077` — file not found at app-render/app-render.tsx
- `app-render.tsx:2568` — file not found at app-render.tsx
- `pages-handler.ts:244` — file not found at pages-handler.ts
- `render.tsx:1622` — file not found at render.tsx
- `render.tsx:457` — file not found at render.tsx
- `base-server.ts:1852` — file not found at base-server.ts
- `next-server.ts:870` — file not found at next-server.ts
- `load-components.ts:355` — file not found at load-components.ts
- `load-components.ts:160` — file not found at load-components.ts
- `base-server.ts:397` — file not found at base-server.ts
- `next-server.ts:634` — file not found at next-server.ts
- `route-modules/route-module.ts:1130` — file not found at route-modules/route-module.ts
- `base-server.ts:2579` — file not found at base-server.ts
- `next-server.ts:616` — file not found at next-server.ts
- `app-render.tsx:3077` — file not found at app-render.tsx
- `app-render.tsx:3145` — file not found at app-render.tsx
- `app-render.tsx:3155` — file not found at app-render.tsx
- `pages-handler.ts:553` — file not found at pages-handler.ts
- `next.ts:NextServer` — file not found at next.ts
- `next-dev-server.ts:122` — file not found at next-dev-server.ts

## sense

### sense/axum  — 65/67 grounded

**Unresolved**
- `handler/mod.rs:208` — file not found at handler/mod.rs
- `routing/method_routing.rs:1355` — file not found at routing/method_routing.rs

### sense/flask  — 50/51 grounded

**Unresolved**
- `test_basic.py:298` — file not found at test_basic.py

### sense/javalin  — 23/38 grounded

**Unresolved**
- `DefaultTasks.kt:14` — file not found at DefaultTasks.kt
- `DefaultTasks.kt:44` — file not found at DefaultTasks.kt
- `InternalRouter.kt:77` — file not found at InternalRouter.kt
- `DefaultTasks.kt:16` — file not found at DefaultTasks.kt
- `InternalRouter.kt:109` — file not found at InternalRouter.kt
- `ExceptionMapper.kt:34` — file not found at ExceptionMapper.kt
- `InternalRouter.kt:115` — file not found at InternalRouter.kt
- `JavalinState.kt:77` — file not found at JavalinState.kt
- `JavalinDefaultRoutingApi.kt:104` — file not found at JavalinDefaultRoutingApi.kt
- `DefaultTasks.kt:84` — file not found at DefaultTasks.kt
- `InternalRouter.kt:100` — file not found at InternalRouter.kt
- `DefaultTasks.kt:18` — file not found at DefaultTasks.kt
- `DefaultTasks.kt:79` — file not found at DefaultTasks.kt
- `DefaultTasks.kt:90` — file not found at DefaultTasks.kt
- `ExceptionMapper.kt:29` — file not found at ExceptionMapper.kt

### sense/nextjs  — 31/44 grounded

**Unresolved**
- `next-server.ts:658` — file not found at next-server.ts
- `next-server.ts:635` — file not found at next-server.ts
- `module.ts:210` — file not found at module.ts
- `app-render.tsx:3066` — file not found at app-render.tsx
- `route-modules/app-page/module.ts:155` — file not found at route-modules/app-page/module.ts
- `module.render.ts:3` — file not found at module.render.ts
- `export/routes/app-page.ts:78` — file not found at export/routes/app-page.ts
- `base-server.ts:1035` — file not found at base-server.ts
- `request-meta.ts:356` — file not found at request-meta.ts
- `app-render.tsx:3077` — file not found at app-render.tsx
- `render.tsx:1622` — file not found at render.tsx
- `work-async-storage.external.ts:130` — file not found at work-async-storage.external.ts
- `base-server.ts:2787` — file not found at base-server.ts

## serena

### serena/axum  — 22/23 grounded

**Unresolved**
- `routing/method_routing.rs:1355` — file not found at routing/method_routing.rs

### serena/discourse  — 45/48 grounded

**Unresolved**
- `category_list_serializer.rb:12` — file not found at category_list_serializer.rb
- `topic_list_serializer.rb:23` — file not found at topic_list_serializer.rb
- `grouped_search_result_serializer.rb:31` — file not found at grouped_search_result_serializer.rb

### serena/javalin  — 11/88 grounded

**Unresolved**
- `Javalin.java:50` — file not found at Javalin.java
- `Javalin.java:128` — file not found at Javalin.java
- `Javalin.java:83` — file not found at Javalin.java
- `config/JavalinState.kt:47` — file not found at config/JavalinState.kt
- `http/servlet/JavalinServlet.kt:31` — file not found at http/servlet/JavalinServlet.kt
- `router/InternalRouter.kt:26` — file not found at router/InternalRouter.kt
- `router/JavalinDefaultRoutingApi.kt:38` — file not found at router/JavalinDefaultRoutingApi.kt
- `config/RoutesConfig.kt:24` — file not found at config/RoutesConfig.kt
- `http/servlet/JavalinServletContext.kt:69` — file not found at http/servlet/JavalinServletContext.kt
- `http/servlet/DefaultTasks.kt:14` — file not found at http/servlet/DefaultTasks.kt
- `router/matcher/PathMatcher.kt:13` — file not found at router/matcher/PathMatcher.kt
- `JavalinDefaultRoutingApi.kt:104` — file not found at JavalinDefaultRoutingApi.kt
- `RoutesConfig.kt:34` — file not found at RoutesConfig.kt
- `InternalRouter.kt:42` — file not found at InternalRouter.kt
- `PathMatcher.kt:20` — file not found at PathMatcher.kt
- `JavalinServlet.kt:37` — file not found at JavalinServlet.kt
- `JavalinServlet.kt:43` — file not found at JavalinServlet.kt
- `JavalinServlet.kt:55` — file not found at JavalinServlet.kt
- `JavalinServlet.kt:56` — file not found at JavalinServlet.kt
- `JavalinState.kt:83` — file not found at JavalinState.kt
- `JavalinServlet.kt:49` — file not found at JavalinServlet.kt
- `DefaultTasks.kt:16` — file not found at DefaultTasks.kt
- `DefaultTasks.kt:22` — file not found at DefaultTasks.kt
- `DefaultTasks.kt:45` — file not found at DefaultTasks.kt
- `DefaultTasks.kt:57` — file not found at DefaultTasks.kt
- `DefaultTasks.kt:75` — file not found at DefaultTasks.kt
- `DefaultTasks.kt:84` — file not found at DefaultTasks.kt
- `DefaultTasks.kt:88` — file not found at DefaultTasks.kt
- `InternalRouter.kt:77` — file not found at InternalRouter.kt
- `matcher/PathMatcher.kt:50` — file not found at matcher/PathMatcher.kt
- `PathMatcher.kt:58` — file not found at PathMatcher.kt
- `router/ParsedEndpoint.kt:14` — file not found at router/ParsedEndpoint.kt
- `JavalinServlet.kt:66` — file not found at JavalinServlet.kt
- `JavalinServlet.kt:106` — file not found at JavalinServlet.kt
- `router/exception/ExceptionMapper.kt:34` — file not found at router/exception/ExceptionMapper.kt
- `JavalinServlet.kt:80` — file not found at JavalinServlet.kt
- `JavalinServlet.kt:116` — file not found at JavalinServlet.kt
- `InternalRouter.kt:115` — file not found at InternalRouter.kt
- `ExceptionMapper.kt:60` — file not found at ExceptionMapper.kt
- `Javalin.java:21` — file not found at Javalin.java
- `config/JavalinState.kt:57` — file not found at config/JavalinState.kt
- `router/JavalinDefaultRoutingApi.kt:104` — file not found at router/JavalinDefaultRoutingApi.kt
- `JavalinDefaultRoutingApi.kt:79` — file not found at JavalinDefaultRoutingApi.kt
- `config/RoutesConfig.kt:34` — file not found at config/RoutesConfig.kt
- `router/InternalRouter.kt:42` — file not found at router/InternalRouter.kt
- `InternalRouter.kt:43` — file not found at InternalRouter.kt
- `router/matcher/PathMatcher.kt:20` — file not found at router/matcher/PathMatcher.kt
- `RoutesConfig.kt:53` — file not found at RoutesConfig.kt
- `router/JavalinDefaultRoutingApi.kt:51` — file not found at router/JavalinDefaultRoutingApi.kt
- `config/RoutesConfig.kt:30` — file not found at config/RoutesConfig.kt
- `InternalRouter.kt:85` — file not found at InternalRouter.kt
- `router/error/ErrorMapper.kt:19` — file not found at router/error/ErrorMapper.kt
- `http/servlet/DefaultTasks.kt:84` — file not found at http/servlet/DefaultTasks.kt
- `InternalRouter.kt:93` — file not found at InternalRouter.kt
- `ErrorMapper.kt:23` — file not found at ErrorMapper.kt
- `JavalinDefaultRoutingApi.kt:44` — file not found at JavalinDefaultRoutingApi.kt
- `RoutesConfig.kt:26` — file not found at RoutesConfig.kt
- `InternalRouter.kt:100` — file not found at InternalRouter.kt
- `router/exception/ExceptionMapper.kt:26` — file not found at router/exception/ExceptionMapper.kt
- `ExceptionMapper.kt:34` — file not found at ExceptionMapper.kt
- `JavalinServlet.kt:60` — file not found at JavalinServlet.kt
- `config/RouterConfig.kt:22` — file not found at config/RouterConfig.kt
- `ExceptionMapper.kt:48` — file not found at ExceptionMapper.kt
- `JavalinServlet.kt:69` — file not found at JavalinServlet.kt
- `DefaultTasks.kt:18` — file not found at DefaultTasks.kt
- `DefaultTasks.kt:79` — file not found at DefaultTasks.kt
- `JavalinServletContext.kt:69` — file not found at JavalinServletContext.kt
- `DefaultTasks.kt:44` — file not found at DefaultTasks.kt
- `PathMatcher.kt:50` — file not found at PathMatcher.kt
- `ParsedEndpoint.kt:14` — file not found at ParsedEndpoint.kt
- `RoutesConfig.kt:24` — file not found at RoutesConfig.kt
- `router/InternalRouter.kt:85` — file not found at router/InternalRouter.kt
- `http/servlet/JavalinServlet.kt:106` — file not found at http/servlet/JavalinServlet.kt
- `InternalRouter.kt:109` — file not found at InternalRouter.kt
- `ExceptionMapper.kt:28` — file not found at ExceptionMapper.kt
- `router/exception/ExceptionMapper.kt:55` — file not found at router/exception/ExceptionMapper.kt
- `router/exception/ExceptionMapper.kt:28` — file not found at router/exception/ExceptionMapper.kt

### serena/nextjs  — 18/35 grounded

**Unresolved**
- `next-server.ts:634` — file not found at next-server.ts
- `next-server.ts:1260` — file not found at next-server.ts
- `base-server.ts:1690` — file not found at base-server.ts
- `base-server.ts:2966` — file not found at base-server.ts
- `next-server.ts:308` — file not found at next-server.ts
- `route-module.ts:1102` — file not found at route-module.ts
- `route-modules/app-page/module.render.ts:3` — file not found at route-modules/app-page/module.render.ts
- `app-render.tsx:3077` — file not found at app-render.tsx
- `render.tsx:1631` — file not found at render.tsx
- `module.ts:9` — file not found at module.ts
- `next-server.ts:635` — file not found at next-server.ts
- `app-render.tsx:417` — file not found at app-render.tsx
- `app-render.tsx:2693` — file not found at app-render.tsx
- `base-server.ts:886` — file not found at base-server.ts
- `app-render.tsx:3145` — file not found at app-render.tsx
- `packages/next/src/server/send-response.ts:confirm` — `confirm` not found anywhere in packages/next/src/server/send-response.ts
- `app-render.tsx:3108` — file not found at app-render.tsx
