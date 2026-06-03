package scantest

// HarvestFixture is a small multi-language working tree that exercises the
// harvested-name machinery end to end: every per-language set (dispatch and
// mention names, partitioned by language) and the flat per-feature sets (cgo
// exports, the four Rust sets, both TS sets, the Python sets, and the langspec
// annotation set). It is the shared home the scan-pipeline split (27-06) and the
// dead-code per-language work (27-09) both scan, so the write side (scan's
// partition) and the read side (dead's per-language voices) are proven against
// one fixture rather than two drifting copies.
//
// Each file is deliberately minimal — just enough source for the real extractor
// to emit the name kinds documented beside it. Adding a name kind here benefits
// both callers; keep the source the smallest that still triggers the extractor.
var HarvestFixture = map[string]string{
	// Ruby: a literal `public_send(:handle)` is a reflective dispatch target;
	// every identifier becomes a mention.
	"lib/dispatcher.rb": `class Dispatcher
  def run(name)
    send(name)
    public_send(:handle)
  end

  def handle; end
end
`,
	// Python: __all__ declares public exports; the decorators feed py_decorated
	// and py_routes.
	"app/views.py": `__all__ = ['index', 'home']

def route(p):
    def d(f):
        return f
    return d

@route('/')
def index():
    return 'i'

@app.get('/home')
def home():
    return 'h'
`,
	// Go: a cgo //export directive feeds cgo_exports.
	"src/ffi.go": `package ffi

import "C"

//export DoThing
func DoThing() {}
`,
	// Rust: #[no_mangle] export, #[allow(dead_code)] retained item, #[test]
	// symbol, and a trait-impl method — one of each Rust set.
	"src/lib.rs": `#[no_mangle]
pub extern "C" fn exported() {}

#[allow(dead_code)]
fn retained() {}

#[test]
fn it_works() {}

trait T { fn m(&self); }
struct S;
impl T for S { fn m(&self) {} }
`,
	// TypeScript: a class decorator feeds ts_decorated; export default feeds
	// ts_default_exports.
	"web/comp.ts": `@Component({})
export class Widget { @Input() x = 1; }

export default function main() {}
`,
	// Java (langspec): annotations feed langspec_annotated.
	"svc/Service.java": `@Service
public class Greeter {
  @Override
  public String hello() { return "hi"; }
}
`,
}
