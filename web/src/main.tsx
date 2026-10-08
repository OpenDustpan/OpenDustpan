import { StrictMode } from "react";
import { createRoot } from "react-dom/client";

import "./design-system/tokens.css";
import "./design-system/components.css";
import "./app.css";
import { App } from "./App";

const root = document.querySelector("#root");
if (root === null) {
  throw new Error("index.html is missing #root");
}

createRoot(root).render(
  <StrictMode>
    <App />
  </StrictMode>,
);
