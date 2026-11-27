"use strict";

const fs = require("node:fs");
const path = require("node:path");

const dir = __dirname;
for (const name of fs.readdirSync(dir)) {
  if (name.endsWith(".test.js")) require(path.join(dir, name));
}
