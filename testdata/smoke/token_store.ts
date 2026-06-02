// This file is an ES module (it exports configureTokens), so TokenStore below
// is genuinely module-private. TokenStore carries an @Injectable decorator, so a
// framework's DI container instantiates it with no source caller. The TS voice
// keeps it possibly_dead (ts_decorator) — proving the decorator harvest rescues a
// module-private class the soundness gate would otherwise call dead.
export function configureTokens(): void {}

@Injectable()
class TokenStore {}
