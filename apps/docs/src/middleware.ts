import { defineMiddleware } from 'astro:middleware'

import config from '../gt.config.json'
import { redirects } from './utils/redirects'

const knownLocales = new Set(config.locales)

export const onRequest = defineMiddleware(
  ({ request, redirect, rewrite }, next) => {
    const url = new URL(request.url)
    const path = url.pathname.replace(/\/$/, '')

    if (path === '/docs/sitemap.xml') {
      return next(new Request(new URL('/docs/sitemap-index.xml', url), request))
    }

    // Match /docs/old-slug or /docs/{locale}/old-slug
    const match = path.match(/^\/docs(?:\/([a-z]{2}))?\/(.+)$/)
    if (match) {
      const locale = match[1]
      const slug = match[2]
      const newSlug = redirects[slug]
      if (newSlug) {
        const target = locale
          ? `/docs/${locale}/${newSlug}`
          : `/docs/${newSlug}`
        return redirect(target, 301)
      }
    }

    const localeMatch = path.match(/^\/docs\/([a-zA-Z][^/.]*)(\/.*)?$/)
    if (localeMatch && !knownLocales.has(localeMatch[1])) {
      return rewrite(
        `/docs/${config.defaultLocale}/${localeMatch[1]}${localeMatch[2] || '/'}`
      )
    }

    return next()
  }
)
