import { createRequire } from 'module'

import express from 'express'

import { handler as ssrHandler } from '../server/entry.mjs'
import { env } from './util/environment.mjs'
import { redirects as slugRedirects } from './util/redirects.mjs'

const require = createRequire(import.meta.url)
const gtConfig = require('../gt.config.json')
const knownLocales = new Set(gtConfig.locales)

// Full-path redirect map from the shared URL map (slug redirects)
const redirects = Object.fromEntries(
  Object.entries(slugRedirects).map(([from, to]) => [`/docs/${from}`, `/docs/${to}`])
)

const app = express()
app.use(express.json())
app.use((req, res, next) => {
  res.setHeader('X-Frame-Options', 'SAMEORIGIN')
  next()
})
app.use((req, res, next) => {
  const path = req.path.replace(/\/$/, '') || req.path
  const target = redirects[path]
  if (target) {
    return res.redirect(301, target)
  }
  // Handle locale-prefixed paths (/docs/en/slug -> /docs/en/new-slug)
  const localeMatch = path.match(/^\/docs\/([a-z]{2})\/(.+)$/)
  if (localeMatch) {
    const bareTarget = redirects[`/docs/${localeMatch[2]}`]
    if (bareTarget) {
      return res.redirect(301, bareTarget.replace('/docs/', `/docs/${localeMatch[1]}/`))
    }
  }
  next()
})
app.use('/docs', express.static('client/'))
app.use((req, res, next) => {
  const match = req.path.match(/^\/docs\/([a-zA-Z][^/.]*)(\/.*)?$/)
  if (match && !knownLocales.has(match[1])) {
    req.url = `/docs/en/${match[1]}${match[2] || '/'}`
  }
  next()
})
app.use(ssrHandler)
app.use((req, res) => {
  res.sendFile('404.html', { root: 'client/' })
})

app.listen(env.FUNCTIONS_PORT, () => {
  console.log(`Functions available on port ${env.FUNCTIONS_PORT}`)
})
