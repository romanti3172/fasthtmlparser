// Dumps what z3l.FastHtmlParser (.NET) makes of each input page, with positions as UTF-8 byte offsets,
// so that the Go port can be compared with it tag by tag.
using System.Text;
using System.Text.Json;
using z3l.FastHtmlParser;

var input = args[0];
var output = args[1];
Directory.CreateDirectory(output);
string[] attributes = ["class", "id", "href", "title", "value", "alt", "data-a", "x", "a", "src"];

foreach (var file in Directory.GetFiles(input, "*.html").Order())
{
    var source = File.ReadAllText(file, new UTF8Encoding(false));
    var doc = new HtmlParser(source);

    // byte offset of each char index
    var bytes = new int[source.Length + 1];
    for (int i = 0; i < source.Length; i++)
    {
        bytes[i + 1] = bytes[i] + (char.IsHighSurrogate(source[i]) ? 4 : char.IsLowSurrogate(source[i]) ? 0 : Encoding.UTF8.GetByteCount(source.AsSpan(i, 1)));
    }
    int Last(int charIndex) => charIndex == 0 ? 0 : bytes[charIndex + 1] - 1; // last byte of the char at charIndex

    var tags = doc.ToList();
    var index = new Dictionary<ParserTag, int>(ReferenceEqualityComparer.Instance);
    for (int i = 0; i < tags.Count; i++) index[tags[i]] = i;

    var rows = tags.Select(t => new Dictionary<string, object?>
    {
        ["name"] = t.Name,
        ["parent"] = t.Parent() is { } p ? index[p] : -1,
        ["depth"] = t.Depth,
        ["start"] = bytes[t.Start],
        ["end"] = Last(t.End),
        ["innerStart"] = t.InnerStart == 0 ? 0 : bytes[t.InnerStart],
        ["innerEnd"] = t.InnerEnd == 0 ? 0 : Last(t.InnerEnd),
        ["text"] = t.Text() ?? "",
        ["attrs"] = attributes.ToDictionary(a => a, a => t.AttributeByName(a)),
    }).ToList();

    File.WriteAllText(Path.Combine(output, Path.GetFileNameWithoutExtension(file) + ".json"),
        JsonSerializer.Serialize(rows), new UTF8Encoding(false));
}
Console.WriteLine($"dumped {Directory.GetFiles(input, "*.html").Length} pages");
