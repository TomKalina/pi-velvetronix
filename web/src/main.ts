// Entry point: the real device (HTTP API and WebSocket bridge on the same host).

import { Api } from "./api";
import { runApp } from "./app";
import { WsTransport } from "./ws";

runApp(document.getElementById("app")!, new Api(), new WsTransport());
