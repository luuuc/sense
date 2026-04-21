const TestPanelPhi = React.lazy(() => import("./TestPanelPhi"));

async function loadTestModuleChi() {
  const mod = await import("./test-utils-psi");
  return mod;
}

import("./test-side-effect-omega");
