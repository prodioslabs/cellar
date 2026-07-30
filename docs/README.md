# Cellar Docs

Fumadocs + Next.js documentation site for Cellar.

## Develop

```bash
bun install
bun run dev
```

Open [http://localhost:3000](http://localhost:3000). Documentation lives under `/docs`.

## Build

```bash
bun run build
bun run start
```

## Layout

| Path               | Purpose                                         |
| ------------------ | ----------------------------------------------- |
| `src/`             | Next.js App Router, layouts, search, components |
| `content/docs/`    | MDX documentation                               |
| `public/`          | Static assets (logo)                            |
| `source.config.ts` | Fumadocs MDX collections                        |
