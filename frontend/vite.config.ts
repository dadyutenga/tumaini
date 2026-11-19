import tailwindcss from "@tailwindcss/vite";
import react from "@vitejs/plugin-react";
import { defineConfig, loadEnv } from "vite";
export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, process.cwd(), "API_PROXY_");
  return {
    resolve: { tsconfigPaths: true },
    build: {
      rolldownOptions: {
        output: {
          codeSplitting: {
            groups: [
              { name: "react-vendor", test: /node_modules\/(react|react-dom|scheduler)\// },
              { name: "radix-vendor", test: /node_modules\/@radix-ui\// },
            ],
          },
        },
      },
    },
    plugins: [react(), tailwindcss()],
    server: {
      // The refresh cookie is SameSite=Strict, so the browser must see one origin. Proxy the API.
      proxy: {
        "/api": {
          target: process.env.API_PROXY_TARGET ?? env.API_PROXY_TARGET ?? "http://127.0.0.1:8080",
          changeOrigin: false,
        },
      },
    },
  };
});
