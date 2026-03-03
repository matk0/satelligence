require "net/http"

class ApiController < ApplicationController
  include ActionController::Live
  skip_forgery_protection

  # Internal Go API base URL
  GO_API_BASE = ENV.fetch("GO_API_URL", "http://localhost:8080")

  # POST /api/webln/quote
  def webln_quote
    response = post_to_go("/v1/webln/quote", request.raw_post)
    proxy_response(response)
  end

  # POST /api/webln/chat
  def webln_chat
    response = post_to_go("/v1/webln/chat/completions", nil, {
      "X-Payment-Hash" => request.headers["X-Payment-Hash"]
    })
    proxy_response(response, %w[X-Charged-Sats X-Cost-Sats X-Refund-Sats])
  end

  # POST /api/webln/chat/stream - SSE streaming endpoint
  def webln_chat_stream
    uri = URI("#{GO_API_BASE}/v1/webln/chat/completions/stream")

    response.headers["Content-Type"] = "text/event-stream"
    response.headers["Cache-Control"] = "no-cache"
    response.headers["Connection"] = "keep-alive"
    response.headers["X-Accel-Buffering"] = "no"

    http = Net::HTTP.new(uri.host, uri.port)
    http.read_timeout = 300

    req = Net::HTTP::Post.new(uri.path)
    req["Content-Type"] = "application/json"
    req["X-Payment-Hash"] = request.headers["X-Payment-Hash"]

    http.request(req) do |upstream_response|
      # Copy charged sats header if present
      if upstream_response["X-Charged-Sats"]
        response.headers["X-Charged-Sats"] = upstream_response["X-Charged-Sats"]
      end

      upstream_response.read_body do |chunk|
        response.stream.write(chunk)
      end
    end
  rescue StandardError => e
    Rails.logger.error("Stream error: #{e.message}")
    response.stream.write("event: error\ndata: {\"error\": \"#{e.message}\"}\n\n")
  ensure
    response.stream.close
  end

  # POST /api/webln/refund
  def webln_refund
    response = post_to_go("/v1/webln/refund", request.raw_post)
    proxy_response(response)
  end

  # POST /api/nwc/chat
  def nwc_chat
    response = post_to_go("/v1/nwc/chat/completions", request.raw_post, {
      "X-NWC" => request.headers["X-NWC"]
    })
    proxy_response(response, %w[X-Charged-Sats X-Cost-Sats X-Cost-USD X-Refund-Sats X-Refund-Status])
  end

  # POST /api/nwc/chat/stream - SSE streaming endpoint for NWC
  def nwc_chat_stream
    uri = URI("#{GO_API_BASE}/v1/nwc/chat/completions/stream")

    response.headers["Content-Type"] = "text/event-stream"
    response.headers["Cache-Control"] = "no-cache"
    response.headers["Connection"] = "keep-alive"
    response.headers["X-Accel-Buffering"] = "no"

    http = Net::HTTP.new(uri.host, uri.port)
    http.read_timeout = 300

    req = Net::HTTP::Post.new(uri.path)
    req["Content-Type"] = "application/json"
    req["X-NWC"] = request.headers["X-NWC"]
    req.body = request.raw_post

    http.request(req) do |upstream_response|
      upstream_response.read_body do |chunk|
        response.stream.write(chunk)
      end
    end
  rescue StandardError => e
    Rails.logger.error("NWC stream error: #{e.message}")
    response.stream.write("event: error\ndata: {\"error\": \"#{e.message}\"}\n\n")
  ensure
    response.stream.close
  end

  # GET /api/models
  def models
    response = get_from_go("/v1/models")
    proxy_response(response)
  end

  # GET /api/debug
  def debug
    response = get_from_go("/debug")
    proxy_response(response)
  end

  private

  def post_to_go(path, body, extra_headers = {})
    uri = URI("#{GO_API_BASE}#{path}")
    http = Net::HTTP.new(uri.host, uri.port)
    http.read_timeout = 120

    req = Net::HTTP::Post.new(uri.path)
    req["Content-Type"] = "application/json"
    extra_headers.each { |k, v| req[k] = v if v.present? }
    req.body = body if body

    http.request(req)
  rescue StandardError => e
    Rails.logger.error("Go API error: #{e.message}")
    nil
  end

  def get_from_go(path)
    uri = URI("#{GO_API_BASE}#{path}")
    Net::HTTP.get_response(uri)
  rescue StandardError => e
    Rails.logger.error("Go API error: #{e.message}")
    nil
  end

  def proxy_response(response, expose_headers = [])
    if response.nil?
      render json: { error: { message: "Backend unavailable" } }, status: :bad_gateway
      return
    end

    # Copy exposed headers
    expose_headers.each do |header|
      value = response[header]
      self.response.headers[header] = value if value
    end

    render json: response.body, status: response.code.to_i
  end
end
