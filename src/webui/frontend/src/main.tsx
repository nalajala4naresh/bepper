import React from "react";
import { createRoot } from "react-dom/client";
import { App } from "./app";

import "./components/input.css";
import "./components/spinner.css";
import "./terminal/terminal.css";
import "./style.css";

createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    <App />
  </React.StrictMode>
);
