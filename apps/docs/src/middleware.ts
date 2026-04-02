import { defineMiddleware } from 'astro:middleware'

import {
  loadDocsMarkdownBody,
  parseDocsContentPath,
  preferredMarkdownPlainFormat,
  shouldTryMarkdownPath,
} from './utils/acceptMarkdownNegotiation'
import { redirects } from './utils/redirects'

function filterProxyHeaders(headers: Headers): Headers {
  const filteredHeaders = new Headers()
  for (const [key, value] of headers.entries()) {
    if (
      !['content-encoding', 'content-length', 'transfer-encoding'].includes(
        key.toLowerCase()
      )
    ) {
      filteredHeaders.set(key, value)
    }
  }
  return filteredHeaders
}

/** Merge `Accept` into `Vary` so caches do not serve HTML for markdown requests or vice versa. */
function withVaryAccept(response: Response): Response {
  const headers = new Headers(response.headers)
  const existing = headers.get('Vary')
  if (existing) {
    const parts = existing.split(',').map(s => s.trim().toLowerCase())
    if (!parts.includes('accept')) {
      headers.set('Vary', `${existing}, Accept`)
    }
  } else {
    headers.set('Vary', 'Accept')
  }
  return new Response(response.body, {
    status: response.status,
    statusText: response.statusText,
    headers,
  })
}

export const onRequest = defineMiddleware(
  async ({ request, redirect }, next) => {
    const url = new URL(request.url)
    const path = url.pathname.replace(/\/$/, '')

    const proxyRequest = async (targetUrl: URL): Promise<Response> => {
      try {
        const response = await fetch(targetUrl.toString(), {
          method: request.method,
          body:
            request.method === 'GET' || request.method === 'HEAD'
              ? undefined
              : request.body,
        })

        return new Response(response.body, {
          status: response.status,
          statusText: response.statusText,
          headers: filterProxyHeaders(response.headers),
        })
      } catch {
        // During prerender the target server doesn't exist yet; fall through
        return next()
      }
    }

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

    const textFormat = preferredMarkdownPlainFormat(
      request.headers.get('accept')
    )
    if (
      textFormat &&
      (request.method === 'GET' || request.method === 'HEAD') &&
      shouldTryMarkdownPath(url.pathname)
    ) {
      const parsed = parseDocsContentPath(url.pathname)
      if (parsed) {
        const body = await loadDocsMarkdownBody(parsed)
        if (body !== null) {
          const contentType =
            textFormat === 'plain'
              ? 'text/plain; charset=utf-8'
              : 'text/markdown; charset=utf-8'
          const headers = {
            'Content-Type': contentType,
            'Cache-Control': 'public, max-age=300',
            Vary: 'Accept',
          } as const
          if (request.method === 'HEAD') {
            return new Response(null, { status: 200, headers })
          }
          return new Response(body, {
            status: 200,
            headers,
          })
        }
      }
    }

    const isNegotiableDocsRequest =
      (request.method === 'GET' || request.method === 'HEAD') &&
      shouldTryMarkdownPath(url.pathname) &&
      parseDocsContentPath(url.pathname) !== null

    const response = await next()
    if (isNegotiableDocsRequest) {
      return withVaryAccept(response)
    }
    return response
  }
)
