import adapter from '@sveltejs/adapter-static';
import { vitePreprocess } from '@sveltejs/vite-plugin-svelte';

/** @type {import('@sveltejs/kit').Config} */
const config = {
	preprocess: vitePreprocess(),
	kit: {
		// Builds to static files, embedded in the controller binary via go:embed
		adapter: adapter({
			pages: '../controller/web/dist',
			assets: '../controller/web/dist',
			fallback: 'index.html',
			strict: false
		})
	}
};

export default config;
