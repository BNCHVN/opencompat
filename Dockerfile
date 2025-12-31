FROM gcr.io/distroless/static:nonroot
COPY opencompat /opencompat
USER nonroot:nonroot
ENV XDG_DATA_HOME=/home/nonroot/.local/share
ENV OPENCOMPAT_HOST=0.0.0.0
ENTRYPOINT ["/opencompat"]
CMD ["serve"]
