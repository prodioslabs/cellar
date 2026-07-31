import { createMDX } from 'fumadocs-mdx/next'

const withMDX = createMDX()

/** @type {import('next').NextConfig} */
const config = {
  reactStrictMode: true,
  output: 'standalone',
  async redirects() {
    return [
      {
        source: '/install.sh',
        destination: 'https://raw.githubusercontent.com/prodioslabs/cellar/main/install.sh',
        permanent: false,
      },
    ]
  },
}

export default withMDX(config)
