// Cloudflare Worker: serves Apple App Site Association file at /.well-known/apple-app-site-association
// Deploy via Cloudflare Dashboard → Workers Routes → Create Worker
// Route: userail.money/.well-known/apple-app-site-association

const AASA = JSON.stringify({
  webcredentials: {
    apps: ["com.railmoney.rail"]
  }
});

export default {
  async fetch(request) {
    return new Response(AASA, {
      headers: {
        "Content-Type": "application/json",
        "Cache-Control": "public, max-age=86400",
      },
    });
  },
};
