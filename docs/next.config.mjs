import { createMDX } from 'fumadocs-mdx/next'

const withMDX = createMDX()

/** @type {import('next').NextConfig} */
const config = {
  reactStrictMode: true,
  output: 'standalone',
  transpilePackages: ['three', '@react-three/fiber'],
  async redirects() {
    return [
      {
        source: '/install.sh',
        destination: 'https://raw.githubusercontent.com/prodioslabs/cellar/main/install.sh',
        permanent: false,
      },
      {
        source: '/docs/architecture/binaries',
        destination: '/docs/architecture#binaries',
        permanent: true,
      },
      {
        source: '/docs/architecture/roles',
        destination: '/docs/architecture#roles',
        permanent: true,
      },
      {
        source: '/docs/architecture/ports',
        destination: '/docs/architecture#ports-and-sockets',
        permanent: true,
      },
      {
        source: '/docs/architecture/cluster-ca',
        destination: '/docs/architecture#cluster-ca',
        permanent: true,
      },
    ]
  },
}

export default withMDX(config)
